package main

import (
	"crypto/ed25519"
	"encoding/binary"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"slices"
	"sync"
	"syscall"
	"time"
)

const (
	tickInterval   = time.Second
	sessionTimeout = 5 * time.Second
)

type Node struct {
	cfg      *Config
	reg      *Registry
	key      DHKey
	conn     *net.UDPConn
	tun      *Tun
	log      *slog.Logger
	mu       sync.Mutex
	sessions map[uint16]*Session
	peers    map[uint16]*Session
	packetID uint64
	sig      ed25519.PrivateKey
	ads      map[uint16]*Known
	adIDs    map[uint16]uint64
	routes   map[uint16][]uint16
	subnet   *net.IPNet
}

func newNode(cfg *Config, log *slog.Logger) *Node {
	reg := newRegistry(cfg.Registry)
	me := reg.byIndex[cfg.Index]

	if me == nil {
		throwFmt("index %d not in registry", cfg.Index)
	}

	dh, sig := deriveKeys(decodeKey(cfg.Key))

	n := &Node{
		cfg:      cfg,
		reg:      reg,
		key:      dh,
		sig:      sig,
		log:      log,
		ads:      map[uint16]*Known{},
		adIDs:    map[uint16]uint64{},
		routes:   map[uint16][]uint16{},
		sessions: map[uint16]*Session{},
		peers:    map[uint16]*Session{},
		packetID: uint64(time.Now().UnixNano()),
	}

	if string(n.key.public) != string(me.pub) {
		throwFmt("private key does not match registry entry %d", cfg.Index)
	}

	if string(sig.Public().(ed25519.PublicKey)) != string(me.sig) {
		throwFmt("signing key does not match registry entry %d", cfg.Index)
	}

	for index, peer := range reg.byIndex {
		if index != cfg.Index {
			n.peers[index] = newSession(me, peer, dh.private)
		}
	}

	_, n.subnet = throw3(net.ParseCIDR(cfg.Subnet))

	n.conn = throw2(net.ListenUDP("udp", &net.UDPAddr{Port: cfg.Port}))
	n.tun = openTun(cfg.Tun, me.intip, cfg.Subnet, cfg.Mtu)

	return n
}

func (n *Node) run() {
	go n.loop("recv", n.recvLoop)
	go n.loop("tun", n.tunLoop)
	go n.loop("status", n.statusLoop)
	go n.loop("signal", n.signalLoop)

	n.loop("timer", n.timerLoop)
}

func (n *Node) signalLoop() {
	signals := make(chan os.Signal, 1)

	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	n.log.Info("stopping", "signal", <-signals)
	os.Exit(0)
}

func (n *Node) loop(name string, body func()) {
	try(body).catch(func(e *Exception) {
		n.log.Error("loop failed", "loop", name, "err", e)
		os.Exit(1)
	})
}

func (n *Node) send(packet []byte, addr *net.UDPAddr) {
	n.conn.WriteToUDP(packet, addr)
}

func (n *Node) install(s *Session) {
	n.sessions[s.peer] = s
	s.created = s.lastRecv
	n.log.Info("link up", "peer", s.peer, "endpoint", s.endpoint)

	n.recompute()
	n.syncTo(s.peer)
	n.publish(time.Now())
}

func (n *Node) remove(s *Session, why string) {
	delete(n.sessions, s.peer)
	n.log.Info("link down", "peer", s.peer, "why", why)

	n.recompute()
	n.publish(time.Now())
}

func (n *Node) recvLoop() {
	buf := make([]byte, maxPacket)

	for {
		size, addr := throw3(n.conn.ReadFromUDP(buf))
		packet := buf[:size]

		if size == 0 {
			continue
		}

		n.mu.Lock()

		if packet[0] == packetTransport {
			n.handleTransport(packet, addr)
		}

		n.mu.Unlock()
	}
}

func (n *Node) handleTransport(packet []byte, addr *net.UDPAddr) {
	if len(packet) < headerTransport {
		return
	}

	s := n.peers[binary.LittleEndian.Uint16(packet[1:])]

	if s == nil {
		return
	}

	inner, ok := s.open(packet)

	if !ok {
		return
	}

	if binary.LittleEndian.Uint64(packet[3:]) == s.window.top {
		changed := s.observed == nil || s.observed.String() != addr.String()

		s.observed = addr

		if changed {
			n.chooseEndpoint(s)
		}
	}

	s.lastRecv = time.Now()

	if n.sessions[s.peer] != s {
		n.install(s)
	}

	if len(inner) == 0 {
		return
	}

	switch inner[0] {
	case innerData:
		n.handleData(inner)
	case innerAd:
		n.handleAd(inner, s.peer, binary.LittleEndian.Uint64(packet[3:]) == s.window.top)
	}
}

func (n *Node) handleData(inner []byte) {
	d, ok := decodeData(inner)

	if !ok || d.path[d.cursor] != n.cfg.Index {
		return
	}

	if d.cursor == len(d.path)-1 {
		if !validIPv4(d.ip) {
			return
		}

		n.tun.write(d.ip)

		return
	}

	d.cursor++
	advanceCursor(inner, d.cursor)
	n.forward(d.path[d.cursor], inner)
}

func (n *Node) forward(peer uint16, inner []byte) {
	s := n.peers[peer]

	if s == nil || s.endpoint == nil {
		return
	}

	n.send(s.seal(inner, n.nextPacketID()), s.endpoint)
}

func (n *Node) chooseEndpoint(s *Session) {
	if s.observed == nil {
		return
	}

	if time.Since(s.seenAt) < sessionTimeout {
		seen := s.seen

		if slices.Contains(seen, s.observed.String()) {
			s.endpoint = s.observed

			return
		}

		if s.endpoint != nil && slices.Contains(seen, s.endpoint.String()) {
			return
		}

		if len(seen) > 0 {
			for _, addr := range n.candidates(n.reg.byIndex[s.peer]) {
				if slices.Contains(seen, addr.String()) {
					s.endpoint = addr

					return
				}
			}
		}
	}

	s.endpoint = s.observed
}

func (n *Node) tunLoop() {
	buf := make([]byte, maxPacket)

	for {
		ip := n.tun.read(buf)

		if len(ip) < 20 || ip[0]>>4 != 4 {
			continue
		}

		peer := n.reg.byIntip[[4]byte(ip[16:20])]

		if peer == nil || peer.index == n.cfg.Index {
			continue
		}

		n.mu.Lock()

		if path := n.route(peer.index); path != nil {
			n.forward(path[0], encodeData(&Data{src: n.cfg.Index, path: path, ip: ip}))
		}

		n.mu.Unlock()
	}
}

func (n *Node) route(dst uint16) []uint16 {
	return n.routes[dst]
}

func (n *Node) timerLoop() {
	for now := range time.Tick(tickInterval) {
		n.mu.Lock()
		n.tick(now)
		n.mu.Unlock()
	}
}

func (n *Node) tick(now time.Time) {
	for _, s := range n.sessions {
		if now.Sub(s.lastRecv) >= sessionTimeout {
			n.remove(s, "timeout")
		}
	}

	n.expire(now)

	for _, peer := range n.peers {
		n.chooseEndpoint(peer)
	}

	n.publish(now)
}

func (n *Node) candidates(peer *Peer) []*net.UDPAddr {
	addrs := slices.Clone(peer.static)
	texts := []string{}

	if known := n.ads[peer.index]; known != nil {
		texts = append(texts, known.ad.Addrs...)
	}

	if addr := n.peers[peer.index].observed; addr != nil {
		texts = append(texts, addr.String())
	}

	for _, text := range texts {
		addr, err := net.ResolveUDPAddr("udp", text)

		if err != nil || n.subnet.Contains(addr.IP) {
			continue
		}

		same := func(a *net.UDPAddr) bool { return a.String() == addr.String() }

		if !slices.ContainsFunc(addrs, same) {
			addrs = append(addrs, addr)
		}
	}

	return addrs
}

func (n *Node) nextPacketID() uint64 {
	n.packetID++

	return n.packetID
}
