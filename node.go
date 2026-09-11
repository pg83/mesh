package main

import (
	"crypto/ed25519"
	"encoding/binary"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/ipv4"
)

const (
	tickInterval   = time.Second
	sessionTimeout = 5 * time.Second
)

type UDPSocket struct {
	conn *ipv4.PacketConn
	port uint16
}

type LocalEndpoint struct {
	socket  *UDPSocket
	address Endpoint
	iface   int
}

type Node struct {
	cfg        *Config
	reg        *Registry
	key        DHKey
	sockets    map[uint16]*UDPSocket
	endpoints  []SocketEndpoint
	incoming   map[Endpoint]Endpoint
	tun        *Tun
	log        *slog.Logger
	mu         sync.Mutex
	peers      map[uint16]*Session
	packetID   uint64
	sig        ed25519.PrivateKey
	graph      map[Edge]*Update
	owned      map[Edge]bool
	observed   map[Edge]time.Time
	local      map[Endpoint]*LocalEndpoint
	discovered map[uint16]map[Endpoint]time.Time
	owners     map[Endpoint]uint16
	routes     map[Endpoint][]Edge
	subnet     *net.IPNet
}

func newNode(cfg *Config, log *slog.Logger) *Node {
	reg := newRegistry(cfg.Registry)
	me := reg.byIndex[cfg.Index]

	if me == nil {
		throwFmt("index %d not in registry", cfg.Index)
	}

	dh, sig := deriveKeys(decodeKey(cfg.Key))

	n := &Node{
		cfg: cfg, reg: reg, key: dh, sig: sig, log: log,
		graph: map[Edge]*Update{}, owned: map[Edge]bool{}, observed: map[Edge]time.Time{},
		discovered: map[uint16]map[Endpoint]time.Time{}, owners: map[Endpoint]uint16{},
		routes: map[Endpoint][]Edge{}, peers: map[uint16]*Session{},
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
			n.discovered[index] = map[Endpoint]time.Time{}
		}
	}

	_, n.subnet = throw3(net.ParseCIDR(cfg.Subnet))
	n.sockets = map[uint16]*UDPSocket{}

	for _, config := range append(append([]EndpointConfig{}, cfg.Endpoint...), me.endpoints...) {
		config.validate()

		if config.Proto != "udp" {
			throwFmt("transport %s is not implemented", config.Proto)
		}

		addr := config.address()
		bind := config.binding()

		if addr.IP.To4() == nil || bind.IP.To4() == nil {
			continue
		}

		local := endpoint(bind.IP, bind.Port)

		if n.sockets[local.Port] == nil {
			n.sockets[local.Port] = newUDPSocket(local.Port)
		}

		n.endpoints = append(n.endpoints, SocketEndpoint{public: endpoint(addr.IP, addr.Port), bind: local})
	}

	if len(n.sockets) == 0 {
		throwFmt("no UDP endpoints configured")
	}

	n.tun = openTun(cfg.Tun, me.intip, cfg.Subnet, cfg.Mtu)
	n.refresh(time.Now())

	return n
}

func (n *Node) run() {
	for _, socket := range n.sockets {
		go n.loop("recv", func() { n.recvLoop(socket) })
	}

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

func (n *Node) send(packet []byte, edge Edge) {
	local := n.local[edge.From]

	if local == nil {
		return
	}

	local.socket.conn.WriteTo(packet, &ipv4.ControlMessage{Src: local.address.ip(), IfIndex: local.iface}, edge.To.addr())
}

func (n *Node) recvLoop(socket *UDPSocket) {
	buf := make([]byte, maxPacket)

	for {
		size, control, addr, err := socket.conn.ReadFrom(buf)

		throw(err)

		if size == 0 || control == nil || control.Dst == nil {
			continue
		}

		remote := addr.(*net.UDPAddr)
		wire := endpoint(control.Dst, int(socket.port))

		n.mu.Lock()

		destination, exists := n.incoming[wire]

		if !exists {
			n.mu.Unlock()

			continue
		}

		edge := Edge{From: endpoint(remote.IP, remote.Port), To: destination}

		if buf[0] == packetTransport || buf[0] == packetGossip {
			n.handleTransport(buf[:size], edge)
		}

		n.mu.Unlock()
	}
}

func (n *Node) handleTransport(packet []byte, edge Edge) {
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

	now := time.Now()
	_, exists := n.observed[edge]

	n.observed[edge] = now
	n.discovered[s.peer][edge.From] = now

	if !exists {
		n.owned[edge] = true
		n.record(edge, true)
		n.recompute()
		n.log.Info("link up", "from", edge.From.string(), "to", edge.To.string())
	}

	if len(inner) == 0 {
		return
	}

	switch inner[0] {
	case innerData:
		n.handleData(inner, edge)
	case innerAd:
		n.handleAd(inner)
	}
}

func (n *Node) handleData(inner []byte, received Edge) {
	d, ok := decodeData(inner)

	if !ok || d.path[d.cursor] != received {
		return
	}

	if d.cursor == len(d.path)-1 {
		if !validIPv4(d.ip) {
			return
		}

		if endpoint(net.IP(d.ip[16:20]), 0) != n.reg.byIndex[n.cfg.Index].endpoint() {
			return
		}

		n.tun.write(d.ip)

		return
	}

	d.cursor++
	advanceCursor(inner, d.cursor)
	n.forward(d.path[d.cursor], inner)
}

func (n *Node) forward(edge Edge, inner []byte) {
	s := n.peers[n.owners[edge.To]]

	if s == nil {
		return
	}

	n.send(s.seal(inner, n.nextPacketID()), edge)
}

func (n *Node) tunLoop() {
	buf := make([]byte, maxPacket)

	for {
		ip := n.tun.read(buf)

		if !validIPv4(ip) {
			continue
		}

		dst := endpoint(net.IP(ip[16:20]), 0)

		n.mu.Lock()

		if path := n.routes[dst]; len(path) != 0 {
			n.forward(path[0], encodeData(&Data{path: path, ip: ip}))
		}

		n.mu.Unlock()
	}
}

func (n *Node) timerLoop() {
	for now := range time.Tick(tickInterval) {
		n.mu.Lock()
		n.refresh(now)

		for _, endpoints := range n.discovered {
			for ep, received := range endpoints {
				if now.Sub(received) > sessionTimeout {
					delete(endpoints, ep)
				}
			}
		}

		n.publish()
		n.mu.Unlock()
	}
}

func (n *Node) nextPacketID() uint64 {
	n.packetID++

	return n.packetID
}
