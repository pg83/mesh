package main

import (
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	tickInterval   = time.Second
	sessionTimeout = 5 * time.Second
)

type UDPSocket struct {
	guard    io.Closer
	conn     *net.UDPConn
	read     func([]byte) (int, net.IP, net.Addr, error)
	port     uint16
	implicit bool
}

type SocketKey struct {
	addr string
	port uint16
	ipv6 bool
}

type LocalAddress struct {
	address SocketAddress
	iface   int
	receive bool
}

type Node struct {
	cfg           *Config
	reg           *Registry
	key           DHKey
	log           *slog.Logger
	subnet        *net.IPNet
	sockets       map[SocketKey]*UDPSocket
	listeners     map[string]*WSListener
	tlsCA         map[uint64]string
	noDial        map[DialPair]bool
	endpoints     []ListenerBinding
	tun           *Tun
	events        *Mailbox[any]
	tunInbox      *Mailbox[any]
	tunWrites     *Mailbox[[]byte]
	channels      map[Edge]*Channel
	snapshot      *Snapshot
	channelStatus map[Edge]ChannelStatus
	dials         map[DialKey]*DialAttempt
	interfaces    InterfaceState
	packetID      uint64
	transportID   atomic.Uint64
	graph         map[Edge]bool
	records       map[uint16]*GraphRecord
	observed      map[Edge]time.Time
	local         map[uint64]*LocalAddress
	addresses     map[uint64]Vertex
	owners        map[uint64]uint16
	seen          map[uint64][]SocketAddress
	routes        map[uint64][]Edge
	hops          map[uint64][]uint16
	next          map[uint16]Edge
	metrics       Metrics
	sshd          *SSHServer
}

func newNode(cfg *Config, log *slog.Logger) *Node {
	reg := newRegistry(cfg.Registry, cfg.RegistryVersion)
	me := reg.byIndex[cfg.Index]

	if me == nil {
		throwFmt("index %d not in registry", cfg.Index)
	}

	dh := deriveKey(decodeKey(cfg.Key))

	n := &Node{
		events: newMailbox[any](nil), tunInbox: newMailbox[any](nil), tunWrites: newMailbox[[]byte](nil), channels: map[Edge]*Channel{},
		cfg: cfg, reg: reg, key: dh, log: log,
		graph: map[Edge]bool{}, records: map[uint16]*GraphRecord{}, observed: map[Edge]time.Time{},
		owners:   map[uint64]uint16{},
		seen:     map[uint64][]SocketAddress{},
		routes:   map[uint64][]Edge{},
		packetID: uint64(time.Now().UnixNano()), addresses: map[uint64]Vertex{},
		channelStatus: map[Edge]ChannelStatus{}, dials: map[DialKey]*DialAttempt{}, listeners: map[string]*WSListener{}, tlsCA: map[uint64]string{},
		noDial: map[DialPair]bool{},
	}

	for _, pair := range cfg.NoDial {
		n.noDial[pair] = true
	}

	n.transportID.Store(uint64(time.Now().UnixNano()))

	if string(n.key.public) != string(me.pub) {
		throwFmt("private key does not match registry entry %d", cfg.Index)
	}

	for index, peer := range reg.byIndex {
		if index != cfg.Index {
			peer.session = newSession(me, peer, dh.private)
		}

		n.remember(peer.vertex())

		for _, config := range peer.endpoints {
			if config.TLSCA != "" {
				n.tlsCA[config.description().hash()] = config.TLSCA
			}
		}

		for _, ep := range peer.addresses {
			n.remember(ep.vertex())
		}
	}

	_, n.subnet = throw3(net.ParseCIDR(cfg.Subnet))

	n.sockets = map[SocketKey]*UDPSocket{}
	n.local = map[uint64]*LocalAddress{}

	for _, config := range append(append([]EndpointConfig{}, cfg.Endpoint...), me.endpoints...) {
		config.validate()

		public := config.description()
		bind := config.binding()

		if public.Addr == "" || bind.IP == nil || bind.IP.IsLinkLocalUnicast() {
			continue
		}

		local := socketAddress(bind.IP, bind.Port)

		n.endpoints = append(n.endpoints, ListenerBinding{config: config, public: public, bind: local})
	}

	n.tun = openTun(cfg.Tun, me.intip, cfg.Subnet, cfg.Mtu)

	if cfg.Sshd {
		n.sshd = newSSHServer(n, cfg, me.intip)
	}

	n.refresh(time.Now())

	return n
}

func (n *Node) run() {
	n.publishSnapshot()
	go n.loop("interfaces", n.watchInterfaces)

	go n.loop("TUN reader", n.readTun)
	go n.loop("TUN actor", n.tunLoop)
	go n.loop("TUN writer", func() {
		for p := range n.tunWrites.out {
			n.tun.write(p)
		}
	})
	go n.loop("control", n.controlLoop)
	go n.loop("signal", func() { stopOnSignal(n.log) })

	if n.sshd != nil {
		go n.loop("sshd", n.sshd.run)
	}

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
	return n.reg.byIndex[peer].session
}
