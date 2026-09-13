package main

import (
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
	guard net.Listener
	conn  *ipv4.PacketConn
	port  uint16
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
	log        *slog.Logger
	subnet     *net.IPNet
	sockets    map[uint16]*UDPSocket
	listeners  map[string]*WSListener
	tlsCA      map[uint64]string
	endpoints  []SocketEndpoint
	tun        *Tun
	events     *Mailbox[any]
	tunInbox   *Mailbox[any]
	tunWrites  *Mailbox[[]byte]
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

	dh := deriveKey(decodeKey(cfg.Key))

	n := &Node{
		events: newMailbox[any](nil), tunInbox: newMailbox[any](nil), tunWrites: newMailbox[[]byte](nil), actors: map[Edge]*EdgeActor{},
		cfg: cfg, reg: reg, key: dh, log: log,
		graph: map[Edge]State{}, owned: map[Edge]bool{}, observed: map[Edge]time.Time{},
		discovered: map[uint16]map[uint64]time.Time{}, owners: map[uint64]uint16{},
		routes:   map[uint64][]Edge{},
		packetID: uint64(time.Now().UnixNano()), addresses: map[uint64]Endpoint{},
		ws: map[Edge]WSStatus{}, dialing: map[Edge]bool{}, listeners: map[string]*WSListener{}, tlsCA: map[uint64]string{},
	}

	if string(n.key.public) != string(me.pub) {
		throwFmt("private key does not match registry entry %d", cfg.Index)
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
	n.publishSnapshot()

	for _, listener := range n.listeners {
		go n.loop("websocket listener", func() { throw(listener.server.Serve(listener.conn)) })
	}

	for _, socket := range n.sockets {
		go n.loop("UDP discovery", func() { n.discoverUDP(socket) })
	}

	go n.loop("TUN reader", n.readTun)
	go n.loop("TUN actor", n.tunLoop)
	go n.loop("TUN writer", func() {
		for p := range n.tunWrites.out {
			n.tun.write(p)
		}
	})
	go n.loop("control", n.controlLoop)
	go n.loop("signal", func() { stopOnSignal(n.log) })
	n.loop("graph", n.graphLoop)
}

func stopOnSignal(log *slog.Logger) {
	signals := make(chan os.Signal, 1)

	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	log.Info("stopping", "signal", <-signals)
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
