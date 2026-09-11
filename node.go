package main

import (
	"crypto/ed25519"
	"log/slog"
	"net"
	"os"
	"os/signal"
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
	sig        ed25519.PrivateKey
	log        *slog.Logger
	subnet     *net.IPNet
	sockets    map[uint16]*UDPSocket
	listeners  map[string]*WSListener
	tlsCA      map[uint64]string
	endpoints  []SocketEndpoint
	tun        *Tun
	events     chan any
	tunInbox   chan any
	tunWrites  chan []byte
	actors     map[Edge]*EdgeActor
	snapshot   *Snapshot
	ws         map[Edge]WSStatus
	dialing    map[Edge]bool
	packetID   uint64
	graph      map[Edge]State
	owned      map[Edge]bool
	observed   map[Edge]time.Time
	discovered map[uint16]map[uint64]time.Time
	local      map[uint64]*LocalEndpoint
	incoming   map[Endpoint]uint64
	addresses  map[uint64]Endpoint
	owners     map[uint64]uint16
	routes     map[uint64][]Edge
}

func newNode(cfg *Config, log *slog.Logger) *Node {
	reg := newRegistry(cfg.Registry)
	me := reg.byIndex[cfg.Index]

	if me == nil {
		throwFmt("index %d not in registry", cfg.Index)
	}

	dh, sig := deriveKeys(decodeKey(cfg.Key))

	n := &Node{
		events: make(chan any, 1024), tunInbox: make(chan any, 1024), tunWrites: make(chan []byte, 1024), actors: map[Edge]*EdgeActor{},
		cfg: cfg, reg: reg, key: dh, sig: sig, log: log,
		graph: map[Edge]State{}, owned: map[Edge]bool{}, observed: map[Edge]time.Time{},
		discovered: map[uint16]map[uint64]time.Time{}, owners: map[uint64]uint16{},
		routes:   map[uint64][]Edge{},
		packetID: uint64(time.Now().UnixNano()), addresses: map[uint64]Endpoint{},
		ws: map[Edge]WSStatus{}, dialing: map[Edge]bool{}, listeners: map[string]*WSListener{}, tlsCA: map[uint64]string{},
	}

	if string(n.key.public) != string(me.pub) {
		throwFmt("private key does not match registry entry %d", cfg.Index)
	}

	if string(sig.Public().(ed25519.PublicKey)) != string(me.sig) {
		throwFmt("signing key does not match registry entry %d", cfg.Index)
	}

	for index, peer := range reg.byIndex {
		n.remember(peer.endpoint())

		for _, config := range peer.endpoints {
			if config.TLSCA != "" {
				n.tlsCA[config.description().hash()] = config.TLSCA
			}
		}

		for _, ep := range peer.addresses {
			n.remember(ep)
		}

		if index != cfg.Index {
			n.discovered[index] = map[uint64]time.Time{}
		}
	}

	_, n.subnet = throw3(net.ParseCIDR(cfg.Subnet))
	n.sockets = map[uint16]*UDPSocket{}

	for _, config := range append(append([]EndpointConfig{}, cfg.Endpoint...), me.endpoints...) {
		config.validate()

		public := config.description()
		bind := config.binding()

		if public.Addr == "" || bind.IP.To4() == nil {
			continue
		}

		local := endpoint(bind.IP, bind.Port)

		if config.Proto == "udp" {
			if n.sockets[local.Port] == nil {
				n.sockets[local.Port] = newUDPSocket(local.Port)
			}
		} else {
			n.listenWS(config)
		}

		n.endpoints = append(n.endpoints, SocketEndpoint{public: public, bind: local})
	}

	if len(n.endpoints) == 0 {
		throwFmt("no endpoints configured")
	}

	n.tun = openTun(cfg.Tun, me.intip, cfg.Subnet, cfg.Mtu)
	n.refresh(time.Now())

	return n
}

func (n *Node) run() {
	n.publishSnapshot(true)

	for _, listener := range n.listeners {
		go n.loop("websocket listener", func() { throw(listener.server.Serve(listener.conn)) })
	}

	for _, socket := range n.sockets {
		go n.loop("UDP discovery", func() { n.discoverUDP(socket) })
	}

	go n.loop("TUN reader", n.readTun)
	go n.loop("TUN actor", n.tunLoop)
	go n.loop("TUN writer", func() {
		for p := range n.tunWrites {
			n.tun.write(p)
		}
	})
	go n.loop("status", n.statusLoop)
	go n.loop("signal", n.signalLoop)
	n.loop("graph", n.graphLoop)
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

func (n *Node) nextPacketID() uint64 {
	n.packetID++

	return n.packetID
}

func (n *Node) session(peer uint16) *Session {
	return newSession(n.reg.byIndex[n.cfg.Index], n.reg.byIndex[peer], n.key.private)
}
