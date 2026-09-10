package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/flynn/noise"
)

const (
	tickInterval      = time.Second
	keepaliveInterval = 5 * time.Second
	sessionTimeout    = 15 * time.Second
	handshakeTimeout  = 5 * time.Second
	dialMinDelay      = time.Second
	dialMaxDelay      = 5 * time.Minute
)

type Attempt struct {
	next  time.Time
	delay time.Duration
}

type Node struct {
	cfg      *Config
	reg      *Registry
	key      noise.DHKey
	conn     *net.UDPConn
	tun      *Tun
	log      *slog.Logger
	mu       sync.Mutex
	sessions map[uint16]*Session
	byID     map[uint32]*Session
	pending  map[uint32]*Handshake
	lastInit map[uint16]uint64
	attempts map[string]*Attempt
	sig      ed25519.PrivateKey
	ads      map[uint16]*Known
	routes   map[uint16][]uint16
	subnet   *net.IPNet
	lastAd   time.Time
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
		routes:   map[uint16][]uint16{},
		sessions: map[uint16]*Session{},
		byID:     map[uint32]*Session{},
		pending:  map[uint32]*Handshake{},
		lastInit: map[uint16]uint64{},
		attempts: map[string]*Attempt{},
	}

	if string(n.key.Public) != string(me.pub) {
		throwFmt("private key does not match registry entry %d", cfg.Index)
	}

	if string(sig.Public().(ed25519.PublicKey)) != string(me.sig) {
		throwFmt("signing key does not match registry entry %d", cfg.Index)
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

func (n *Node) freshID() uint32 {
	for {
		var b [4]byte
		throw2(rand.Read(b[:]))

		id := binary.BigEndian.Uint32(b[:])
		_, s := n.byID[id]
		_, h := n.pending[id]

		if id != 0 && !s && !h {
			return id
		}
	}
}

func (n *Node) handshakeState(initiator bool, peerPub []byte) *noise.HandshakeState {
	return throw2(noise.NewHandshakeState(noise.Config{
		CipherSuite:   cipherSuite,
		Pattern:       noise.HandshakeIK,
		Initiator:     initiator,
		Prologue:      []byte(prologue),
		StaticKeypair: n.key,
		PeerStatic:    peerPub,
	}))
}

func (n *Node) send(packet []byte, addr *net.UDPAddr) {
	n.conn.WriteToUDP(packet, addr)
}

func (n *Node) install(s *Session) {
	if old := n.sessions[s.peer]; old != nil {
		delete(n.byID, old.localID)
	}

	n.sessions[s.peer] = s
	n.byID[s.localID] = s
	n.dropPending(s.peer)
	n.forgetAttempts(s.peer)
	n.log.Info("link up", "peer", s.peer, "endpoint", s.endpoint)

	n.recompute()
	n.syncTo(s.peer)
	n.publish(time.Now())
}

func (n *Node) remove(s *Session, why string) {
	delete(n.sessions, s.peer)
	delete(n.byID, s.localID)
	n.forgetAttempts(s.peer)
	n.log.Info("link down", "peer", s.peer, "why", why)

	n.recompute()
	n.publish(time.Now())
}

func (n *Node) dropPending(peer uint16) {
	for id, h := range n.pending {
		if h.peer == peer {
			delete(n.pending, id)
		}
	}
}

func (n *Node) hasPending(peer uint16) bool {
	for _, h := range n.pending {
		if h.peer == peer {
			return true
		}
	}

	return false
}

func (n *Node) forgetAttempts(peer uint16) {
	prefix := attemptKey(peer, "")

	for key := range n.attempts {
		if strings.HasPrefix(key, prefix) {
			delete(n.attempts, key)
		}
	}
}

func attemptKey(peer uint16, addr string) string {
	return fmt.Sprintf("%d:%s", peer, addr)
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

		switch packet[0] {
		case packetInit:
			n.handleInit(packet, addr)
		case packetResponse:
			n.handleResponse(packet, addr)
		case packetTransport:
			n.handleTransport(packet, addr)
		}

		n.mu.Unlock()
	}
}

func (n *Node) handleInit(packet []byte, addr *net.UDPAddr) {
	if len(packet) < headerInit {
		return
	}

	remoteID := binary.BigEndian.Uint32(packet[1:])
	hs := n.handshakeState(false, nil)
	payload, _, _, err := hs.ReadMessage(nil, packet[headerInit:])

	if err != nil || len(payload) != 8 {
		return
	}

	peer := n.reg.byPub[[32]byte(hs.PeerStatic())]

	if peer == nil || peer.index == n.cfg.Index {
		return
	}

	stamp := binary.BigEndian.Uint64(payload)

	if stamp <= n.lastInit[peer.index] {
		return
	}

	if n.hasPending(peer.index) {
		if n.cfg.Index < peer.index {
			return
		}

		n.dropPending(peer.index)
	}

	response, recv, send, err := hs.WriteMessage(nil, nil)

	if err != nil {
		return
	}

	n.lastInit[peer.index] = stamp

	now := time.Now()
	s := newSession(peer.index, n.freshID(), remoteID, send, recv, addr, now)

	n.install(s)

	out := make([]byte, headerResponse, headerResponse+len(response))

	out[0] = packetResponse
	binary.BigEndian.PutUint32(out[1:], remoteID)
	binary.BigEndian.PutUint32(out[5:], s.localID)
	n.send(append(out, response...), addr)
}

func (n *Node) handleResponse(packet []byte, addr *net.UDPAddr) {
	if len(packet) < headerResponse {
		return
	}

	localID := binary.BigEndian.Uint32(packet[1:])
	remoteID := binary.BigEndian.Uint32(packet[5:])
	h := n.pending[localID]

	if h == nil {
		return
	}

	_, send, recv, err := h.state.ReadMessage(nil, packet[headerResponse:])

	if err != nil {
		return
	}

	delete(n.pending, localID)
	n.install(newSession(h.peer, localID, remoteID, send, recv, addr, time.Now()))
}

func (n *Node) handleTransport(packet []byte, addr *net.UDPAddr) {
	if len(packet) < headerTransport {
		return
	}

	s := n.byID[binary.BigEndian.Uint32(packet[1:])]

	if s == nil {
		return
	}

	inner, ok := s.open(packet)

	if !ok {
		return
	}

	s.endpoint = addr
	s.lastRecv = time.Now()

	if len(inner) == 0 {
		return
	}

	switch inner[0] {
	case innerData:
		n.handleData(inner)
	case innerAd:
		n.handleAd(inner, s.peer)
	}
}

func (n *Node) handleData(inner []byte) {
	d, ok := decodeData(inner)

	if !ok || d.path[d.cursor] != n.cfg.Index {
		return
	}

	if d.cursor == len(d.path)-1 {
		n.tun.write(d.ip)

		return
	}

	d.cursor++
	advanceCursor(inner, d.cursor)
	n.forward(d.path[d.cursor], inner)
}

func (n *Node) forward(peer uint16, inner []byte) {
	s := n.sessions[peer]

	if s == nil {
		return
	}

	n.send(s.seal(inner, time.Now()), s.endpoint)
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
		if now.Sub(s.lastRecv) > sessionTimeout {
			n.remove(s, "timeout")
		} else if now.Sub(s.lastSend) > keepaliveInterval {
			n.send(s.seal([]byte{innerKeepalive}, now), s.endpoint)
		}
	}

	for id, h := range n.pending {
		if now.Sub(h.created) > handshakeTimeout {
			delete(n.pending, id)
		}
	}

	n.expire(now)

	if now.Sub(n.lastAd) >= adInterval {
		n.publish(now)
	}

	n.dial(now)
}

func (n *Node) dial(now time.Time) {
	for _, peer := range n.reg.byIndex {
		if peer.index == n.cfg.Index || n.sessions[peer.index] != nil {
			continue
		}

		for _, addr := range n.candidates(peer) {
			key := attemptKey(peer.index, addr.String())
			a := n.attempts[key]

			if a == nil {
				a = &Attempt{delay: dialMinDelay}
				n.attempts[key] = a
			}

			if now.Before(a.next) {
				continue
			}

			a.next = now.Add(a.delay)
			a.delay = min(a.delay*2, dialMaxDelay)
			n.sendInit(peer, addr, now)
		}
	}
}

func (n *Node) candidates(peer *Peer) []*net.UDPAddr {
	addrs := slices.Clone(peer.static)
	known := n.ads[peer.index]

	if known == nil {
		return addrs
	}

	for _, text := range known.ad.Addrs {
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

func (n *Node) sendInit(peer *Peer, addr *net.UDPAddr, now time.Time) {
	hs := n.handshakeState(true, peer.pub)
	stamp := binary.BigEndian.AppendUint64(nil, uint64(now.UnixNano()))
	msg, _, _, err := hs.WriteMessage(nil, stamp)

	if err != nil {
		return
	}

	h := &Handshake{
		peer:    peer.index,
		localID: n.freshID(),
		state:   hs,
		addr:    addr,
		created: now,
	}

	n.pending[h.localID] = h

	out := make([]byte, headerInit, headerInit+len(msg))

	out[0] = packetInit
	binary.BigEndian.PutUint32(out[1:], h.localID)
	n.send(append(out, msg...), addr)
}
