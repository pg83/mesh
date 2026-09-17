package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"
)

const (
	tickInterval   = time.Second
	sessionTimeout = 5 * time.Second

	// How long the node waits before asking again for something the kernel
	// refused it: the interface list once, a socket read for every failure in
	// a row, so that a socket which is truly broken cannot spin.
	interfaceRetry = time.Second
	readRetry      = 10 * time.Millisecond
)

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
	relisten      bool
	listeners     map[string]*WSListener
	listenerIDs   map[Vertex]uint32
	counter       uint32
	tlsCA         map[Endpoint]string
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
	order         *VertexOrder
	alive         map[uint16]bool
	records       map[uint16]*GraphRecord
	vectors       map[uint16]*Vector
	routeCache    map[string]*Route
	observed      map[Edge]time.Time
	local         map[uint32]*LocalAddress
	addresses     map[uint32]Vertex
	seen          map[uint32][]SocketAddress
	routes        map[uint32][]Edge
	hops          map[uint32][]uint16
	next          map[uint16]Edge
	metrics       Metrics
	sshd          *SSHServer
	dns           *DNSServer
	net           *Netstack
	intip         [4]byte
}

type Candidate struct {
	id     uint32
	vertex Vertex
	wire   SocketAddress
}

type UDPSource struct {
	socket *UDPSocket
	local  *LocalAddress
	vertex Vertex
}

func (n *Node) reachable(src InterfaceAddress, target Vertex) bool {
	route := n.routeCache[target.Addr]
	lifetime := routeTTL

	if route != nil && route.partial {
		lifetime = interfaceRetry
	}

	if route == nil || (!route.pending && time.Since(route.at) >= lifetime) {
		if route == nil {
			route = &Route{host: target.Addr}
			n.routeCache[target.Addr] = route
		}

		route.pending = true

		ifaces := []int{}

		for _, iface := range n.interfaces {
			ifaces = append(ifaces, iface.iface)
		}

		go n.resolveRoute(target.Addr, slices.Compact(slices.Sorted(slices.Values(ifaces))))
	}

	if route.at.IsZero() {
		return true
	}

	for _, ip := range route.targets {
		if src.prefix.Contains(ip) {
			return true
		}
	}

	return route.viable[src.iface]
}

func (n *Node) resolveRoute(host string, ifaces []int) {
	route := &Route{host: host, at: time.Now(), viable: map[int]bool{}}

	if ip, err := netip.ParseAddr(host); err == nil {
		route.targets = []netip.Addr{ip.Unmap()}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

		defer cancel()

		if ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host); err == nil {
			for _, ip := range ips {
				route.targets = append(route.targets, ip.Unmap())
			}
		}
	}

	for _, iface := range ifaces {
		for _, ip := range route.targets {
			viable, err := routeViable(iface, ip)

			if err != nil {
				n.log.Warn("route lookup failed", "interface", iface, "target", ip.String(), "err", err)

				route.partial = true

				continue
			}

			if viable {
				route.viable[iface] = true
			}
		}
	}

	post(n.events.in, any(route))
}

type WSListener struct {
	server  *http.Server
	conn    net.Listener
	proto   string
	tls     *tls.Config
	started bool
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
		graph: map[Edge]bool{}, records: map[uint16]*GraphRecord{}, vectors: map[uint16]*Vector{}, routeCache: map[string]*Route{}, observed: map[Edge]time.Time{},
		seen:     map[uint32][]SocketAddress{},
		routes:   map[uint32][]Edge{},
		packetID: uint64(time.Now().UnixNano()), addresses: map[uint32]Vertex{},
		channelStatus: map[Edge]ChannelStatus{}, dials: map[DialKey]*DialAttempt{}, listeners: map[string]*WSListener{}, tlsCA: map[Endpoint]string{},
		listenerIDs: map[Vertex]uint32{}, counter: runtimeVertices,
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

		for _, config := range peer.endpoints {
			if config.TLSCA != "" {
				n.tlsCA[config.description()] = config.TLSCA
			}
		}
	}

	_, n.subnet = throw3(net.ParseCIDR(cfg.Subnet))

	n.sockets = map[SocketKey]*UDPSocket{}
	n.local = map[uint32]*LocalAddress{}

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

	if cfg.Tun != "" {
		n.tun = openTun(cfg.Tun, me.intip, cfg.Subnet, cfg.Mtu)

		for _, route := range cfg.exitRoutes {
			for _, prefix := range routeHalves(route.prefix) {
				n.tun.route(prefix)
			}
		}
	}

	n.intip = me.intip

	if cfg.Sshd || cfg.Dns {
		n.net = newNetstack(n, cfg.Mtu, [][4]byte{me.intip, serviceAddress(n.subnet)})
	}

	if cfg.Sshd {
		n.sshd = newSSHServer(n, cfg, me.intip)
	}

	if cfg.Dns {
		n.dns = newDNSServer(n, cfg)
	}

	n.refresh(time.Now())

	return n
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

func (n *Node) allocate() uint32 {
	n.counter++

	return vertexID(n.cfg.Index, n.counter)
}

func (n *Node) listenerID(public Vertex) uint32 {
	public = public.canonical()

	if id, known := n.listenerIDs[public]; known {
		return id
	}

	me := n.reg.byIndex[n.cfg.Index]

	for i, ep := range me.addresses {
		if ep.vertex() == public {
			n.listenerIDs[public] = me.endpointID(i)

			return me.endpointID(i)
		}
	}

	n.listenerIDs[public] = n.allocate()

	return n.listenerIDs[public]
}

func (n *Node) nextPacketID() uint64 {
	n.packetID++

	return n.packetID
}

func (n *Node) session(peer uint16) *Session {
	return n.reg.byIndex[peer].session
}

func (n *Node) sourceAvailable(local *LocalAddress) bool {
	for _, addr := range n.interfaces {
		if addr.ip == local.address.Addr && addr.iface == local.iface {
			return true
		}
	}

	return false
}

func (n *Node) installChannel(c *ChannelIO) {
	local := c.localID()

	if c.ctx.Err() != nil || n.session(c.peer) != c.session || (c.listener && n.local[local] == nil) || (!c.listener && (c.local == nil || !n.sourceAvailable(c.local))) {
		c.stop()

		return
	}

	old := n.channels[c.edge]

	if old != nil {
		previous := old.io

		if previous.ctx.Err() == nil && (previous.origin < c.origin || (previous.origin == c.origin && previous.id >= c.id)) {
			c.stop()

			return
		}

		previous.stop()
	}

	if c.source.valid() {
		n.addresses[c.edge.From] = c.source
	}

	if c.target.valid() {
		n.addresses[c.edge.To] = c.target
	}

	if !c.listener {
		n.local[local] = c.local
	}

	actor := &Channel{node: n, edge: c.edge, peer: c.peer, outgoing: c.outgoing, session: c.session, io: c, view: n.snapshot}

	n.channels[c.edge] = actor
	n.channelStatus[c.edge] = ChannelStatus{Transport: c.transport(), Edge: c.edge, Outgoing: c.outgoing, ID: c.id, Wire: c.wire}
	go n.loop("channel", actor.run)
}

func (n *Node) syncLocal() {
	n.local = n.scanLocal(n.interfaces)

	for _, actor := range n.channels {
		c := actor.io
		local := c.localID()

		if c.listener {
			if n.local[local] == nil {
				c.stop()
			}
		} else if c.ctx.Err() == nil && n.sourceAvailable(c.local) {
			n.local[local] = c.local
		} else {
			c.stop()
		}
	}
}

func (n *Node) publishSnapshot() {
	n.syncDials()

	channels := maps.Clone(n.channels)
	versions := map[uint16]uint64{}

	for owner, record := range n.records {
		versions[owner] = record.Version
	}

	view := &Snapshot{registry: n.reg, graph: maps.Clone(n.graph), addresses: maps.Clone(n.addresses), local: maps.Clone(n.local),
		routes: n.routes, hops: n.hops, next: n.next, channels: channels, records: versions, alive: n.alive}

	view.exits = map[uint16]bool{}

	for owner, record := range n.records {
		if record.Exit && n.alive[owner] {
			view.exits[owner] = true
		}
	}

	n.publishVector()
	view.vectors = maps.Clone(n.vectors)
	view.gossip = n.advertisements()
	view.bundles = n.bundles()

	n.snapshot = view

	if n.dns != nil {
		n.dns.update(view)
	}

	for _, actor := range n.channels {
		actor.post(view)
	}

	post(n.tunInbox.in, any(view))
}

func (n *Node) observe(r ChannelReport) {
	if n.channels[r.edge] != r.actor || r.session != n.session(r.peer) {
		return
	}

	if r.status == nil {
		if _, exists := n.observed[r.edge]; exists {
			n.metrics.linkDown.Add(1)
		}

		delete(n.observed, r.edge)
	}

	if r.status != nil && !r.seen.IsZero() && time.Since(r.seen) < sessionTimeout && r.seen.After(n.observed[r.edge]) {
		_, exists := n.observed[r.edge]

		n.observed[r.edge] = r.seen

		if !exists {
			n.metrics.linkUp.Add(1)
			n.log.Info("link up", "from", n.describe(r.edge.From), "to", n.describe(r.edge.To))
			n.resetBackoff(r.peer)
		}
	}

	if r.status == nil {
		delete(n.channelStatus, r.edge)
		delete(n.channels, r.edge)
	} else {
		n.channelStatus[r.edge] = *r.status
	}
}

func (n *Node) graphLoop() {
	ticker := time.NewTicker(tickInterval)

	defer ticker.Stop()

	for {
		select {
		case event := <-n.events.out:
			switch v := event.(type) {
			case InterfaceState:
				n.syncListeners(v)
				n.interfaces = v
				clear(n.routeCache)
				n.resetBackoff(0)
				n.syncLocal()
			case *GraphRecord:
				n.handleRecord(v)
			case *Vector:
				n.handleVector(v)
			case *Route:
				n.routeCache[v.host] = v
			case RegistryRecords:
				n.handleRegistry(v)
			case ChannelReport:
				n.observe(v)
			case DialResult:
				if n.dials[v.attempt.key] != v.attempt {
					for _, c := range v.channels {
						c.stop()
					}

					continue
				}

				v.attempt.pending = false
				v.attempt.channels = v.channels

				if len(v.channels) == 0 {
					v.attempt.failures++
					v.attempt.next = time.Now().Add(backoff(v.attempt.failures))
				} else {
					n.resetBackoff(v.attempt.session.peer)
				}

				for _, c := range v.channels {
					n.installChannel(c)
				}
			case *ChannelIO:
				n.installChannel(v)
			case chan *Snapshot:
				post(v, n.snapshot)
			case chan *Status:
				post(v, n.status())
			}
		case now := <-ticker.C:
			n.refresh(now)
			n.publishSnapshot()
		}
	}
}

func (n *Node) currentSnapshot() *Snapshot {
	reply := make(chan *Snapshot, 1)

	n.events.in <- reply

	return <-reply
}

func (n *Node) handleRegistry(records RegistryRecords) {
	reg := n.reg

	for _, record := range records {
		old := reg.byIndex[record.Index]

		if old != nil && old.version >= record.Version {
			continue
		}

		try(func() {
			peer := newPeer(record.PeerConfig, record.Version)
			me := reg.byIndex[n.cfg.Index]

			if peer.index == n.cfg.Index {
				if string(peer.pub) != string(n.key.public) || peer.intip != me.intip {
					return
				}
			} else if old != nil && string(old.pub) == string(peer.pub) {
				peer.session = old.session
			} else {
				peer.session = newSession(me, peer, n.key.private)
			}

			if reg == n.reg {
				reg = reg.copy()
			}

			if old != nil && reg.byIntip[old.intip] == old {
				delete(reg.byIntip, old.intip)
			}

			reg.byIndex[peer.index] = peer
			reg.byIntip[peer.intip] = peer

			n.log.Info("registry updated", "index", peer.index, "version", peer.version)
		}).catch(func(e *Exception) { n.log.Debug("registry record rejected", "index", record.Index, "err", e) })
	}

	n.reg = reg
}

func (n *Node) publishRecord() {
	ingress := map[uint32]bool{}
	egress := map[uint32]bool{}

	for id, local := range n.local {
		if local.receive {
			ingress[id] = true
		}
	}

	for edge, channel := range n.channelStatus {
		if channel.Outgoing {
			if n.local[edge.From] != nil {
				egress[edge.From] = true
			}
		} else if n.local[edge.To] != nil {
			ingress[edge.To] = true
		}
	}

	record := &GraphRecord{Owner: n.cfg.Index, Vertices: []RecordVertex{}, Links: []Edge{}, Observed: []Observation{}}
	exported := map[uint32]bool{}
	observed := map[uint32]SocketAddress{}

	for id := range n.local {
		// The address of a local vertex is written wherever the vertex itself
		// is, so there is always one here.
		vertex := n.addresses[id]

		if ingress[id] || egress[id] {
			record.Vertices = append(record.Vertices, RecordVertex{ID: id, Vertex: vertex, Ingress: ingress[id], Egress: egress[id]})
			exported[id] = true
		}
	}

	for edge := range n.observed {
		if !exported[edge.To] || edge.From == 0 || edge.From == edge.To {
			continue
		}

		record.Links = append(record.Links, edge)

		source, known := n.addresses[edge.From]
		wire := n.channelStatus[edge].Wire

		if wire.Port == 0 || !known || source.Proto != "udp" || socketAddress(source.ip(), int(source.Port)) == wire {
			continue
		}

		if current, exists := observed[edge.From]; !exists || compareSocketAddress(wire, current) < 0 {
			observed[edge.From] = wire
		}
	}

	for from, wire := range observed {
		record.Observed = append(record.Observed, Observation{From: from, Seen: wire.vertex()})
	}

	slices.SortFunc(record.Vertices, func(a, b RecordVertex) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(record.Links, compareEdge)
	slices.SortFunc(record.Observed, func(a, b Observation) int { return cmp.Compare(a.From, b.From) })

	body := encodeRecordBody(record)
	previous := n.records[n.cfg.Index]

	record.Exit = n.cfg.Exit

	if previous != nil && bytes.Equal(previous.body, body) && previous.Exit == record.Exit {
		return
	}

	record.Version = n.nextPacketID()
	record.body = body
	record.packet = compress(encodeRecord(n.cfg.Index, record.Version, recordFlags(record), body))
	record.applied = time.Now()

	n.records[n.cfg.Index] = record
}

func (n *Node) handleRecord(record *GraphRecord) {
	if previous := n.records[record.Owner]; previous != nil && record.Version <= previous.Version {
		n.metrics.recordsStale.Add(1)

		return
	}

	n.metrics.recordsApplied.Add(1)
	record.applied = time.Now()
	n.records[record.Owner] = record
	n.resetBackoff(record.Owner)
}

func (n *Node) rebuild() {
	addresses := map[uint32]Vertex{}
	graph := map[Edge]bool{}
	present := map[uint32]bool{}

	for index, peer := range n.reg.byIndex {
		addresses[hostID(index)] = peer.vertex()

		for i, ep := range peer.addresses {
			addresses[peer.endpointID(i)] = ep.vertex()
		}
	}

	for index, record := range n.records {
		host := hostID(index)

		for _, v := range record.Vertices {
			addresses[v.ID] = v.Vertex
			present[v.ID] = true

			if v.Ingress {
				graph[Edge{From: v.ID, To: host}] = true
			}

			if v.Egress {
				graph[Edge{From: host, To: v.ID}] = true
			}
		}
	}

	listener := func(id uint32) uint32 {
		source := addresses[id]

		for _, v := range n.records[vertexOwner(id)].Vertices {
			if v.Ingress && v.isEndpoint() && v.Proto == "udp" && v.Addr == source.Addr && v.Port == source.Port {
				return v.ID
			}
		}

		return 0
	}

	seen := map[uint32][]SocketAddress{}

	n.alive = n.heard()

	for _, record := range n.records {
		for _, link := range record.Links {
			if present[link.From] && link.From != link.To {
				graph[link] = true
			}
		}

		for _, o := range record.Observed {
			if !present[o.From] {
				continue
			}

			if id := listener(o.From); id != 0 {
				seen[id] = append(seen[id], socketAddress(o.Seen.ip(), int(o.Seen.Port)))
			}
		}
	}

	for id, wires := range seen {
		slices.SortFunc(wires, compareSocketAddress)
		seen[id] = slices.Compact(wires)
	}

	for id := range n.local {
		if v, ok := n.addresses[id]; ok {
			addresses[id] = v
		}
	}

	for _, actor := range n.channels {
		if actor.io.source.valid() {
			addresses[actor.io.edge.From] = actor.io.source
		}

		if actor.io.target.valid() {
			addresses[actor.io.edge.To] = actor.io.target
		}
	}

	for edge := range graph {
		if vertexOwner(edge.From) != vertexOwner(edge.To) && !n.alive[vertexOwner(edge.To)] {
			delete(graph, edge)
		}
	}

	n.addresses = addresses
	n.seen = seen
	n.graph = graph
	n.order = newVertexOrder(addresses, graph)
	n.recompute()
}

func (n *Node) heard() map[uint16]bool {
	alive := map[uint16]bool{n.cfg.Index: true}

	for edge := range n.observed {
		alive[vertexOwner(edge.From)] = true
	}

	for grown := true; grown; {
		grown = false

		for owner, record := range n.records {
			if !alive[owner] {
				continue
			}

			for _, link := range record.Links {
				if from := vertexOwner(link.From); !alive[from] {
					alive[from] = true
					grown = true
				}
			}
		}
	}

	return alive
}

func (n *Node) compareIDs(a, b uint32) int {
	return n.order.compare(a, b)
}

// Only the edges of channels are ranked, and a channel never ends on a host
// vertex, so there is nothing here about those.
func (n *Node) cost(edge Edge) int {
	if n.addresses[edge.To].Proto == "udp" {
		return costUDP
	}

	return costWS
}

func (n *Node) recompute() {
	// A node is in its own registry, the registry is in the order, so this is
	// where the routes start from.
	source := n.order.index(hostID(n.cfg.Index))

	size := len(n.order.ids)
	adjacency := make([][]int32, size)

	// The order is built from this very graph, so both ends of every edge have
	// a place in it.
	for edge := range n.graph {
		from, to := n.order.index(edge.From), n.order.index(edge.To)

		adjacency[from] = append(adjacency[from], to)
	}

	owner := make([]uint16, size)
	attachment := make([]bool, size)
	link := make([]int32, size)

	for i, id := range n.order.ids {
		owner[i] = vertexOwner(id)
		attachment[i] = isHostID(id)
		link[i] = costWS

		if n.addresses[id].Proto == "udp" {
			link[i] = costUDP
		}
	}

	cost := make([]int32, size)
	hops := make([]int32, size)
	prev := make([]int32, size)
	paths := make([][]int32, size)
	seen := make([]bool, size)
	done := make([]bool, size)
	queue := &RouteHeap{}

	seen[source] = true

	queue.push(RouteEntry{id: source})

	for !queue.empty() {
		cur := queue.pop().id

		if done[cur] {
			continue
		}

		done[cur] = true

		for _, next := range adjacency[cur] {
			if done[next] {
				continue
			}

			step := RouteEntry{cost: cost[cur], hops: hops[cur], path: paths[cur], id: next}

			if !attachment[cur] && !attachment[next] {
				step.cost += link[next]
			}

			if owner[cur] != owner[next] {
				step.hops++
			}

			if seen[next] {
				current := RouteEntry{cost: cost[next], hops: hops[next], path: paths[prev[next]], id: next}

				if compareRouteEntry(step, current) >= 0 {
					continue
				}
			}

			step.path = append(append(make([]int32, 0, len(paths[cur])+1), paths[cur]...), next)
			cost[next], hops[next], prev[next], paths[next], seen[next] = step.cost, step.hops, cur, step.path, true

			queue.push(step)
		}
	}

	routes := map[uint32][]Edge{}

	for index := range size {
		if !seen[index] || hops[index] == 0 || hops[index] > maxHops {
			continue
		}

		path := []Edge{}

		for cur := int32(index); cur != source; cur = prev[cur] {
			path = append(path, Edge{From: n.order.ids[prev[cur]], To: n.order.ids[cur]})
		}

		slices.Reverse(path)
		routes[n.order.ids[index]] = path
	}

	n.routes = routes
	n.hops = map[uint32][]uint16{}

	for dst, path := range routes {
		nodes := []uint16{}

		for _, edge := range path {
			if isHostID(edge.To) {
				nodes = append(nodes, vertexOwner(edge.To))
			}
		}

		if len(nodes) != 0 && slices.Max(nodes) < 256 {
			n.hops[dst] = nodes
		}
	}

	n.next = map[uint16]Edge{}

	for edge, status := range n.channelStatus {
		if !status.Outgoing || !n.graph[edge] {
			continue
		}

		peer := vertexOwner(edge.To)
		current, exists := n.next[peer]

		if !exists || n.cost(edge) < n.cost(current) || (n.cost(edge) == n.cost(current) && n.compareIDs(edge.From, current.From) < 0) {
			n.next[peer] = edge
		}
	}
}

func (n *Node) publishVector() {
	records := map[uint16]uint64{}

	for owner, record := range n.records {
		records[owner] = record.Version
	}

	if current := n.vectors[n.cfg.Index]; current != nil && maps.Equal(current.Records, records) {
		return
	}

	n.vectors[n.cfg.Index] = &Vector{Owner: n.cfg.Index, Version: n.nextPacketID(), Records: records}
}

func (n *Node) handleVector(chunk *Vector) {
	current := n.vectors[chunk.Owner]

	switch {
	case current == nil || chunk.Version > current.Version:
		n.metrics.vectorsApplied.Add(1)
		n.vectors[chunk.Owner] = chunk
	case chunk.Version == current.Version:
		merged := &Vector{Owner: chunk.Owner, Version: chunk.Version, Records: maps.Clone(current.Records)}

		maps.Copy(merged.Records, chunk.Records)
		n.vectors[chunk.Owner] = merged
	default:
		n.metrics.vectorsStale.Add(1)
	}
}

func (n *Node) bundles() [][]byte {
	items := []vectorItem{}
	owners := slices.Sorted(maps.Keys(n.vectors))

	if own := n.vectors[n.cfg.Index]; own != nil {
		owners = slices.Insert(slices.DeleteFunc(owners, func(o uint16) bool { return o == n.cfg.Index }), 0, n.cfg.Index)
	}

	for _, owner := range owners {
		v := n.vectors[owner]

		for _, o := range slices.Sorted(maps.Keys(v.Records)) {
			items = append(items, vectorItem{vector: v, owner: o, version: v.Records[o]})
		}
	}

	return packBundles(items)
}

func (n *Node) scanLocal(addresses InterfaceState) map[uint32]*LocalAddress {
	local := map[uint32]*LocalAddress{}
	incoming := map[SocketAddress]uint32{}

	for _, addr := range addresses {
		ip := net.IP(addr.ip.AsSlice())

		for _, config := range n.bindings() {
			if ip.IsLoopback() && !config.bind.ip().IsLoopback() {
				continue
			}

			wire := socketAddress(ip, int(config.bind.Port))

			if config.bind.ipv6() != wire.ipv6() || (!config.bind.ip().IsUnspecified() && config.bind.Addr != wire.Addr) {
				continue
			}

			public := config.public

			if public.ip().IsUnspecified() {
				public.Addr = wire.Addr.String()
			}

			id := n.listenerID(public.vertex())

			if previous, exists := incoming[wire]; public.Proto == "udp" && exists && previous != id {
				throwFmt("ambiguous endpoint binding: %s", wire.string())
			}

			if previous := local[id]; previous != nil && previous.address != wire {
				throwFmt("ambiguous public endpoint: %s", public.string())
			}

			if public.Proto == "udp" {
				incoming[wire] = id
			}

			n.addresses[id] = public.vertex().canonical()
			local[id] = &LocalAddress{address: wire, iface: addr.iface, receive: true}
		}
	}

	return local
}

func (n *Node) refresh(now time.Time) {
	throw(sys.check("panic"))

	if n.relisten {
		n.syncListeners(n.interfaces)
	}

	n.syncLocal()

	for edge, received := range n.observed {
		if now.Sub(received) >= sessionTimeout {
			delete(n.observed, edge)
			n.metrics.linkDown.Add(1)
			n.log.Info("link down", "from", n.describe(edge.From), "to", n.describe(edge.To))
		}
	}

	n.publishRecord()
	n.rebuild()
}

func (n *Node) advertisements() []Advertisement {
	owners := slices.Sorted(maps.Keys(n.records))
	ads := make([]Advertisement, 0, len(owners))

	for _, owner := range owners {
		ads = append(ads, Advertisement{owner: owner, version: n.records[owner].Version, packet: n.records[owner].packet})
	}

	return ads
}

func (n *Node) candidates(peer *Peer) []Candidate {
	ids := map[uint32]Vertex{}

	for i, ep := range peer.addresses {
		ids[peer.endpointID(i)] = ep.vertex().canonical()
	}

	if record := n.records[peer.index]; record != nil && n.alive[peer.index] {
		for _, v := range record.Vertices {
			if v.Ingress && v.isEndpoint() && !n.subnet.Contains(v.ip()) {
				ids[v.ID] = v.Vertex
			}
		}
	}

	out := []Candidate{}

	for _, id := range slices.Sorted(maps.Keys(ids)) {
		out = append(out, Candidate{id: id, vertex: ids[id], wire: n.wire(id, ids[id])})
	}

	return out
}

func (n *Node) wire(id uint32, vertex Vertex) SocketAddress {
	if vertex.Proto != "udp" {
		return SocketAddress{}
	}

	own := socketAddress(vertex.ip(), int(vertex.Port))
	seen := n.seen[id]

	if len(seen) == 0 || !own.Addr.IsPrivate() || n.onLink(own.Addr) {
		return own
	}

	return seen[0]
}

func (n *Node) onLink(addr netip.Addr) bool {
	for _, iface := range n.interfaces {
		if iface.prefix.Contains(addr) {
			return true
		}
	}

	return false
}

func (n *Node) describe(id uint32) string {
	if v, ok := n.addresses[id]; ok {
		return v.string()
	}

	return strconv.FormatUint(uint64(vertexOwner(id)), 10) + "/" + strconv.FormatUint(uint64(vertexCounter(id)), 10)
}

func (n *Node) interfaceAddresses() (InterfaceState, error) {
	interfaces, err := sys.interfaces()

	if err != nil {
		return nil, err
	}

	addresses := InterfaceState{}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Name == n.cfg.Tun {
			continue
		}

		addrs, err := sys.addresses(iface)

		if err != nil {
			// Not being able to read an interface's addresses is not the same
			// as that interface having none. Handing back a state with them
			// missing would close every socket bound to them and drop every
			// channel through them, so the whole scan fails instead and the
			// node keeps what it knew until the next attempt.
			return nil, err
		}

		for _, addr := range addrs {
			prefix := netip.MustParsePrefix(addr.String())
			ip := prefix.Addr().Unmap()

			if (!ip.IsGlobalUnicast() && !ip.IsLoopback()) || n.subnet.Contains(net.IP(ip.AsSlice())) {
				continue
			}

			addresses = append(addresses, InterfaceAddress{ip: ip, prefix: netip.PrefixFrom(ip, prefix.Bits()), iface: iface.Index})
		}
	}

	slices.SortFunc(addresses, func(a, b InterfaceAddress) int {
		if order := cmp.Compare(a.iface, b.iface); order != 0 {
			return order
		}

		return a.ip.Compare(b.ip)
	})

	return addresses, nil
}

func (n *Node) watchInterfaces() {
	socket := openInterfaceEvents()

	defer socket.Close()

	changed := make(chan struct{}, 1)

	go n.loop("interface events", func() {
		buf := make([]byte, 65536)

		for {
			if err := sys.interfaceEvent(socket, buf); err != nil {
				n.log.Warn("interface notification lost", "err", err)
				time.Sleep(time.Second)
			}

			select {
			case changed <- struct{}{}:
			default:
			}
		}
	})

	ticker := time.NewTicker(30 * time.Second)

	defer ticker.Stop()

	var previous InterfaceState

	for {
		current, err := n.interfaceAddresses()

		if err != nil {
			// A scan that fails leaves the node with whatever it knew before,
			// and at startup that is nothing at all: no address to listen on
			// and none to dial from. Ask again shortly instead of sitting out
			// the whole period.
			n.log.Warn("interface scan failed", "err", err)

			select {
			case <-changed:
			case <-time.After(interfaceRetry):
			}

			continue
		}

		if !slices.Equal(previous, current) {
			// What was read a moment ago is not what is there now, and the
			// node is about to act on it.
			sys.pause("interface pause")
			post(n.events.in, any(current))
			previous = current
		}

		select {
		case <-changed:
		case <-ticker.C:
		}
	}
}

func (n *Node) implicitSocket(ip netip.Addr) *SocketKey {
	for key, socket := range n.sockets {
		if socket.implicit && key.addr == ip.String() {
			return &key
		}
	}

	return nil
}

func (n *Node) bindings() []ListenerBinding {
	out := slices.Clone(n.endpoints)

	for key, socket := range n.sockets {
		if socket.implicit {
			ip := netip.MustParseAddr(key.addr)
			bind := SocketAddress{Addr: ip, Port: key.port}

			out = append(out, ListenerBinding{public: Endpoint{Proto: "udp", Addr: ip.String(), Port: key.port}, bind: bind})
		}
	}

	return out
}

func (n *Node) udpSocketFor(wire SocketAddress) *UDPSocket {
	if socket := n.sockets[wire.socketKey()]; socket != nil {
		return socket
	}

	key := wire.socketKey()

	key.addr = key.wildcard()

	return n.sockets[key]
}

func (n *Node) udpSource(src InterfaceAddress) *UDPSource {
	var best *UDPSource

	for id, local := range n.local {
		vertex := n.addresses[id]

		if !local.receive || vertex.Proto != "udp" || !vertex.isEndpoint() || local.address.Addr != src.ip || local.iface != src.iface {
			continue
		}

		// Every local vertex is a listener the node holds open: one that cannot
		// be opened stops the node there and then, so there is no such thing
		// here as a vertex without its socket.
		socket := n.udpSocketFor(local.address)

		if best == nil || (best.socket.implicit && !socket.implicit) || (best.socket.implicit == socket.implicit && compareVertex(vertex, best.vertex) < 0) {
			best = &UDPSource{socket: socket, local: local, vertex: vertex}
		}
	}

	return best
}

func (n *Node) syncListeners(addresses InterfaceState) {
	n.relisten = false
	present := map[string]bool{}

	for _, addr := range addresses {
		present[addr.ip.String()] = true
	}

	udp := map[SocketKey]bool{}
	ws := map[string]bool{}
	wildcard := map[bool]bool{}
	concrete := map[netip.Addr]bool{}
	configs := slices.Clone(n.endpoints)

	slices.SortStableFunc(configs, func(a, b ListenerBinding) int {
		if a.bind.ip().IsUnspecified() && !b.bind.ip().IsUnspecified() {
			return -1
		}

		if !a.bind.ip().IsUnspecified() && b.bind.ip().IsUnspecified() {
			return 1
		}

		return 0
	})

	for _, config := range configs {
		bind := config.bind

		if !bind.ip().IsUnspecified() && !present[bind.Addr.String()] {
			continue
		}

		if config.public.Proto == "udp" {
			key := bind.socketKey()
			any := key

			any.addr = key.wildcard()

			if udp[any] {
				continue
			}

			udp[key] = true

			if bind.ip().IsUnspecified() {
				wildcard[bind.ipv6()] = true
			} else {
				concrete[bind.Addr] = true
			}

			if n.sockets[key] == nil {
				socket := newUDPSocket(key, false)

				n.sockets[key] = socket
				go n.loop("UDP listener", func() { n.discoverUDP(socket) })
			}
		} else {
			ws[n.listenWS(config)] = true
		}
	}

	for _, addr := range addresses {
		if addr.ip.IsLoopback() || wildcard[addr.ip.Is6()] || concrete[addr.ip] {
			continue
		}

		key := n.implicitSocket(addr.ip)

		if key == nil {
			try(func() {
				socket := newUDPSocket(SocketKey{addr: addr.ip.String(), ipv6: addr.ip.Is6()}, true)

				key = &SocketKey{addr: addr.ip.String(), port: socket.port, ipv6: addr.ip.Is6()}
				n.sockets[*key] = socket
				go n.loop("UDP listener", func() { n.discoverUDP(socket) })
			}).catch(func(e *Exception) {
				// The addresses are only looked at again when they change,
				// which may be never. A socket that could not be opened this
				// time is asked for again on the next tick instead.
				n.relisten = true

				n.log.Debug("implicit UDP socket failed", "address", addr.ip.String(), "err", e)
			})
		}

		if key != nil {
			udp[*key] = true
		}
	}

	for key, socket := range n.sockets {
		if !udp[key] {
			socket.conn.Close()

			if socket.guard != nil {
				socket.guard.Close()
			}

			delete(n.sockets, key)
		}
	}

	for addr, listener := range n.listeners {
		if !ws[addr] {
			listener.server.Close()
			delete(n.listeners, addr)

			continue
		}

		if !listener.started {
			listener.started = true
			go n.loop("WS listener", func() {
				err := listener.server.Serve(listener.conn)

				if !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
					throw(err)
				}
			})
		}
	}
}

func (n *Node) resetBackoff(peer uint16) {
	for _, attempt := range n.dials {
		if peer == 0 || attempt.session.peer == peer {
			attempt.failures = 0
			attempt.next = time.Time{}
		}
	}
}

func (n *Node) excludesDial(from, to Vertex) bool {
	src, _ := netip.ParseAddr(from.Addr)
	dst, _ := netip.ParseAddr(to.Addr)

	return n.noDial[DialPair{From: src.Unmap(), To: dst.Unmap()}]
}

func (n *Node) syncDials() {
	desired := map[DialKey]bool{}
	now := time.Now()

	for index, peer := range n.reg.byIndex {
		if index == n.cfg.Index {
			continue
		}

		for _, candidate := range n.candidates(peer) {
			dst, wire := candidate.id, candidate.wire
			target := candidate.vertex
			remote := target

			if target.Proto == "udp" {
				remote = wire.vertex()
			}

			for _, src := range n.interfaces {
				if src.ip.IsLoopback() || (remote.ip() != nil && src.ip.Is6() != remote.ipv6()) || n.excludesDial(Vertex{Addr: src.ip.String()}, remote) || !n.reachable(src, remote) {
					continue
				}

				key := DialKey{source: src, target: dst, vertex: target, wire: wire}

				desired[key] = true

				attempt := n.dials[key]

				if attempt != nil && attempt.session != peer.session {
					for _, c := range attempt.channels {
						c.stop()
					}

					attempt = nil
				}

				if attempt == nil {
					attempt = &DialAttempt{key: key, id: n.allocate(), target: target, wire: wire, session: peer.session, local: &LocalAddress{address: socketAddress(net.IP(src.ip.AsSlice()), 0), iface: src.iface}}
					n.dials[key] = attempt
				}

				alive := false

				for _, c := range attempt.channels {
					alive = alive || c.ctx.Err() == nil
				}

				if !alive && !attempt.pending && !now.Before(attempt.next) {
					if target.Proto == "udp" {
						source := n.udpSource(src)

						if source == nil {
							continue
						}

						attempt.socket, attempt.source, attempt.local = source.socket, source.vertex, source.local
					}

					attempt.pending = true
					attempt.next = now.Add(time.Second)
					go n.dialChannel(attempt)
				}
			}
		}
	}

	for key, attempt := range n.dials {
		if !desired[key] {
			for _, c := range attempt.channels {
				c.stop()
			}

			delete(n.dials, key)
		}
	}
}

func (n *Node) dialChannel(attempt *DialAttempt) {
	result := DialResult{attempt: attempt}

	defer func() {
		for _, c := range result.channels {
			c.dialed = true
		}

		// A dial that takes its time lands in a node that has moved on: the
		// peer may have a new session by now, or the address it was dialled
		// from may be gone.
		sys.pause("dial pause")
		post(n.events.in, any(result))
	}()

	try(func() {
		id := n.transportID.Add(1)

		if attempt.target.Proto == "udp" {
			result.channels = []*ChannelIO{newUDPChannel(attempt, id)}

			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

		defer cancel()

		socket, address := n.dialWebSocket(ctx, attempt.local, attempt.target.endpoint())
		source := socketVertex(address)
		ws := newWSConnection(socket, attempt.session, Edge{From: attempt.id, To: attempt.key.target}, source, attempt.target, false, id)
		local := &LocalAddress{address: socketAddress(address.IP, address.Port), iface: attempt.local.iface}

		ws.send.local, ws.receive.local = local, local
		result.channels = []*ChannelIO{ws.send, ws.receive}
	}).catch(func(e *Exception) {
		n.metrics.dialFailed.Add(1)
		n.log.Debug("channel dial failed", "endpoint", attempt.target.string(), "err", e)
	})
}

func (n *Node) discoverUDP(socket *UDPSocket) {
	buf := make([]byte, maxPacket)
	inputs := map[UDPInputKey]UDPInput{}

	defer func() {
		for _, input := range inputs {
			input.channel.stop()
		}
	}()

	failures := 0

	for {
		size, dst, addr, err := sys.readSocket(socket, buf)

		if errors.Is(err, net.ErrClosed) {
			return
		}

		if err != nil {
			// A datagram socket can fail one read and go on working: a queue
			// that filled up, a signal, an ICMP error arriving for something
			// sent earlier. None of that is a reason to take the node down,
			// which is what an exception out of this loop would do.
			failures++

			n.log.Warn("socket read failed", "port", socket.port, "err", err)
			time.Sleep(min(time.Duration(failures)*readRetry, time.Second))

			continue
		}

		failures = 0

		if size < headerTransport || dst == nil {
			continue
		}

		remote := addr.(*net.UDPAddr)
		key := UDPInputKey{remote: socketAddress(remote.IP, remote.Port), local: socketAddress(dst, int(socket.port)), peer: packetSender(buf)}
		input, known := inputs[key]
		now := time.Now()

		var source uint32
		var inner []byte

		if known && input.channel.ctx.Err() == nil {
			var ok bool
			source, inner, ok = input.channel.session.open(buf[:size])

			if !ok {
				continue
			}

			known = source == input.channel.edge.From
		}

		if !known || input.channel.ctx.Err() != nil {
			view := n.currentSnapshot()
			id := n.incomingID(view, key.local)
			session, from, body, ok := n.readPacket(buf[:size], view)

			if !ok || id == 0 {
				continue
			}

			source, inner = from, body

			c := newChannelIO(session, Edge{From: source, To: id}, Vertex{}, view.addresses[id], false, true, packetID(buf))

			c.local = view.local[id]
			c.wire = key.remote

			input.channel = c
			post(n.events.in, any(c))

			for key, old := range inputs {
				if now.Sub(old.seen) >= sessionTimeout {
					old.channel.stop()
					delete(inputs, key)
				}
			}
		}

		input.seen = now
		inputs[key] = input

		select {
		case input.channel.inbox.in <- Received{packet: append([]byte(nil), buf[:headerTransport]...), source: source, inner: inner, at: now, io: input.channel}:
		case <-input.channel.ctx.Done():
		}
	}
}

func (n *Node) incomingID(view *Snapshot, wire SocketAddress) uint32 {
	for id, local := range view.local {
		v := view.addresses[id]

		if v.isEndpoint() && v.Proto == "udp" && local.address == wire {
			return id
		}
	}

	return 0
}

func (n *Node) listenWS(config ListenerBinding) string {
	c := config.config
	proto := c.BindProto

	if proto == "" {
		proto = c.Proto
	}

	if proto != "ws" && proto != "wss" {
		throwFmt("bad bind_proto %q", proto)
	}

	bind := config.bind
	key := bind.socketKey()
	host := bind.Addr.String()
	address := net.JoinHostPort(host, strconv.Itoa(int(key.port)))
	shared := address
	wildcard := net.JoinHostPort(key.wildcard(), strconv.Itoa(int(key.port)))

	if n.listeners[wildcard] != nil {
		shared = wildcard
	}

	if existing := n.listeners[shared]; existing != nil {
		if existing.proto != proto {
			throwFmt("conflicting listener protocols")
		}

		return shared
	}

	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	listener := sys.accepts(throw2(net.Listen(key.network("tcp"), address)))

	if proto == "wss" {
		loaded := map[string]bool{}

		for _, entry := range n.endpoints {
			other := entry.config

			if other.TLSCert == "" || entry.bind.Port != bind.Port || entry.bind.ipv6() != bind.ipv6() || (!bind.ip().IsUnspecified() && entry.bind != bind) || loaded[other.TLSCert] {
				continue
			}

			tlsConfig.Certificates = append(tlsConfig.Certificates, throw2(tls.LoadX509KeyPair(other.TLSCert, other.TLSKey)))
			loaded[other.TLSCert] = true
		}

		if len(tlsConfig.Certificates) == 0 {
			throwFmt("TLS listener needs tls_cert and tls_key")
		}

		listener = tls.NewListener(listener, tlsConfig)
	}

	server := &http.Server{Handler: http.HandlerFunc(n.acceptWS), ReadHeaderTimeout: time.Minute}

	n.listeners[address] = &WSListener{server: server, conn: listener, proto: proto, tls: tlsConfig}

	return address
}

func (n *Node) acceptWS(w http.ResponseWriter, r *http.Request) {
	try(func() {
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)

		defer cancel()

		view := n.currentSnapshot()
		address := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
		host := r.Host

		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}

		var target Vertex
		var listener uint32

		for id, local := range view.local {
			ep := view.addresses[id]

			if ep.isEndpoint() && ep.Proto != "udp" && local.address == socketAddress(address.IP, address.Port) && ep.Path == r.URL.RequestURI() && strings.EqualFold(ep.Addr, host) {
				if listener != 0 {
					throwFmt("ambiguous websocket listener")
				}

				target, listener = ep, id
			}
		}

		if listener == 0 {
			http.NotFound(w, r)

			return
		}

		socket := throw2(websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{protocol}}))

		defer socket.CloseNow()

		socket.SetReadLimit(maxPacket)

		packet := readWS(ctx, socket)
		session, source, _, ok := n.readPacket(packet, view)

		if !ok {
			return
		}

		c := newWSConnection(socket, session, Edge{From: listener, To: source}, target, Vertex{}, true, packetID(packet))

		c.send.local, c.receive.local = view.local[listener], view.local[listener]

		read := c.receive.read

		c.receive.read = func(input chan any) {
			select {
			case input <- Received{packet: packet, at: time.Now(), io: c.receive}:
			case <-c.receive.ctx.Done():
			}

			read(input)
		}

		post(n.events.in, any(c.send))
		post(n.events.in, any(c.receive))

		<-c.done
	}).catch(func(e *Exception) { n.log.Debug("websocket accept failed", "err", e) })
}

func (n *Node) dialWebSocket(ctx context.Context, local *LocalAddress, target Endpoint) (*websocket.Conn, *net.TCPAddr) {
	dialer := &net.Dialer{LocalAddr: &net.TCPAddr{IP: local.address.ip()}, Control: tcpControl(local.iface)}

	var address *net.TCPAddr

	transport := &http.Transport{DialContext: func(ctx context.Context, network, remote string) (net.Conn, error) {
		conn, err := dialer.DialContext(ctx, network, remote)

		if err == nil {
			address = conn.LocalAddr().(*net.TCPAddr)
		}

		return conn, err
	}, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}

	defer transport.CloseIdleConnections()

	if ca := n.tlsCA[target]; ca != "" {
		pool := throw2(x509.SystemCertPool())

		if !pool.AppendCertsFromPEM(throw2(os.ReadFile(ca))) {
			throwFmt("invalid TLS CA")
		}

		transport.TLSClientConfig.RootCAs = pool
	}

	socket, _ := throw3(websocket.Dial(ctx, target.url(), &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}, Subprotocols: []string{protocol}}))

	socket.SetReadLimit(maxPacket)

	return socket, address
}

func (n *Node) readPacket(packet []byte, view *Snapshot) (*Session, uint32, []byte, bool) {
	if len(packet) < headerTransport {
		return nil, 0, nil, false
	}

	peer := packetSender(packet)

	if peer == n.cfg.Index || view.registry.byIndex[peer] == nil {
		return nil, 0, nil, false
	}

	session := view.registry.byIndex[peer].session
	source, inner, ok := session.open(packet)

	if !ok {
		return nil, source, nil, false
	}

	return session, source, inner, true
}

func (n *Node) run() {
	n.publishSnapshot()
	go n.loop("interfaces", n.watchInterfaces)

	go n.loop("TUN actor", n.tunLoop)

	if n.tun != nil {
		go n.loop("TUN reader", n.readTun)
	}

	go n.loop("TUN writer", func() {
		for p := range n.tunWrites.out {
			if n.tun != nil {
				n.tun.write(p)
			} else {
				n.metrics.tunDropped.Add(1)
			}
		}
	})
	go n.loop("control", n.controlLoop)
	go n.loop("signal", func() { stopOnSignal(n.log) })

	if n.sshd != nil {
		go n.loop("sshd", n.sshd.run)
	}

	if n.dns != nil {
		go n.loop("dns", n.dns.run)
	}

	n.loop("graph", n.graphLoop)
}

func (n *Node) routeData(view *Snapshot, d *Data, inner []byte) {
	if d.cursor == len(d.hops) {
		n.metrics.tunDelivered.Add(1)

		if n.net != nil && n.net.accepts(d.payload) {
			n.net.inject(d.payload)
		} else {
			post(n.tunWrites.in, d.payload)
		}

		return
	}

	if actor := view.channels[view.next[d.hops[d.cursor]]]; actor != nil {
		actor.post(Outbound{inner: inner})
	} else {
		n.metrics.forwardNoChannel.Add(1)
	}
}

func (n *Node) readTun() {
	buf := make([]byte, maxPacket)

	for {
		packet := append([]byte(nil), n.tun.read(buf)...)

		if destination := ipDestination(packet); destination != nil {
			post(n.tunInbox.in, any(TunPacket{payload: packet, destination: destination}))
		}
	}
}

func (n *Node) tunLoop() {
	var view *Snapshot

	for message := range n.tunInbox.out {
		switch v := message.(type) {
		case *Snapshot:
			view = v
		case TunPacket:
			n.metrics.tunRead.Add(1)

			if n.net != nil && n.net.accepts(v.payload) {
				n.net.inject(v.payload)

				continue
			}

			var hops []uint16

			if ip := v.destination.To4(); ip != nil {
				if [4]byte(ip) == n.intip {
					post(n.tunWrites.in, v.payload)

					continue
				}

				if peer := view.registry.byIntip[[4]byte(ip)]; peer != nil {
					hops = view.hops[hostID(peer.index)]
				} else if !n.subnet.Contains(ip) {
					hops = n.exitHops(view, ip, v.payload)
				}
			}

			if len(hops) == 0 {
				n.metrics.tunUnrouted.Add(1)

				continue
			}

			d := &Data{hops: hops, payload: v.payload}

			n.routeData(view, d, encodeData(d))
		}
	}
}

func (n *Node) exitHops(view *Snapshot, ip net.IP, packet []byte) []uint16 {
	addr, _ := netip.AddrFromSlice(ip)

	for _, route := range n.cfg.exitRoutes {
		if !route.prefix.Contains(addr) {
			continue
		}

		candidates := []uint16{}

		for _, node := range route.nodes {
			if view.exits[node] && len(view.hops[hostID(node)]) > 0 {
				candidates = append(candidates, node)
			}
		}

		if len(candidates) == 0 {
			continue
		}

		n.metrics.tunExit.Add(1)

		return view.hops[hostID(candidates[flowHash(packet)%uint32(len(candidates))])]
	}

	return nil
}

func (n *Node) status() *Status {
	now := time.Now()
	st := &Status{Index: n.cfg.Index, Subnet: n.cfg.Subnet, Tun: n.cfg.Tun, Links: []LinkStatus{}, Graph: []Edge{}, Records: []*GraphRecord{}, Vectors: []*Vector{}, Vertices: []uint32{}, Routes: map[string][]Edge{}}

	st.Alive = slices.Sorted(maps.Keys(n.alive))
	st.Registry = n.reg.records()
	st.DnsRecords = n.cfg.DnsRecords
	st.Addresses = map[uint32]Vertex{}

	for id, ep := range n.addresses {
		st.Addresses[id] = ep
	}

	st.Channels = []ChannelStatus{}

	for _, attempt := range n.dials {
		if attempt.pending {
			st.Dialing++
		}
	}

	for _, c := range n.channelStatus {
		st.Channels = append(st.Channels, c)
	}

	for edge, received := range n.observed {
		st.Links = append(st.Links, LinkStatus{Edge: edge, Idle: int(now.Sub(received).Seconds())})
	}

	vertices := map[uint32]bool{}

	for edge := range n.graph {
		st.Graph = append(st.Graph, edge)

		vertices[edge.From] = true
		vertices[edge.To] = true
	}

	for _, owner := range slices.Sorted(maps.Keys(n.records)) {
		st.Records = append(st.Records, n.records[owner])
	}

	st.Bundles = len(n.bundles())

	for _, owner := range slices.Sorted(maps.Keys(n.vectors)) {
		st.Vectors = append(st.Vectors, n.vectors[owner])
	}

	for ep := range vertices {
		st.Vertices = append(st.Vertices, ep)
	}

	slices.SortFunc(st.Channels, func(a, b ChannelStatus) int { return compareEdge(a.Edge, b.Edge) })
	slices.Sort(st.Vertices)
	slices.SortFunc(st.Graph, compareEdge)
	slices.SortFunc(st.Links, func(a, b LinkStatus) int { return compareEdge(a.Edge, b.Edge) })

	for dst, path := range n.routes {
		if isHostID(dst) {
			st.Routes[n.describe(dst)] = path
		}
	}

	return st
}

func (n *Node) readStatus() *Status {
	reply := make(chan *Status, 1)

	n.events.in <- reply

	return <-reply
}

func (n *Node) publicRegistry() []PeerConfig {
	peers := []PeerConfig{}

	for _, record := range n.currentSnapshot().registry.records() {
		peers = append(peers, record.PeerConfig)
	}

	return peers
}

func (n *Node) exportConfig(w http.ResponseWriter, r *http.Request) {
	peers := n.publicRegistry()
	name := r.URL.Query().Get("node")

	for _, peer := range peers {
		if name == "" || (name != peer.Name && name != strconv.Itoa(int(peer.Index))) {
			continue
		}

		if len(peer.Endpoint) != 0 {
			http.Error(w, "node has static endpoints", http.StatusBadRequest)

			return
		}

		bootstrap := []PeerConfig{}

		for _, candidate := range peers {
			if candidate.Index == peer.Index || len(candidate.Endpoint) != 0 {
				bootstrap = append(bootstrap, candidate)
			}
		}

		cfg := Config{
			Index: peer.Index, Subnet: n.cfg.Subnet, Mtu: n.cfg.Mtu,
			Control: "127.0.0.1:8058", Registry: bootstrap,
			Endpoint: []EndpointConfig{},
			Dns:      n.cfg.Dns, DnsRecords: n.cfg.DnsRecords,
		}

		w.Header().Set("Content-Disposition", "attachment; filename=mesh-"+strconv.Itoa(int(peer.Index))+".json")
		writeJSON(w, cfg)

		return
	}

	http.Error(w, "unknown node; specify ?node=name or ?node=index", http.StatusNotFound)
}

func (n *Node) controlLoop() {
	if n.cfg.Control == "" {
		return
	}

	address := controlAddress(n.cfg.Control)
	mux := http.NewServeMux()

	mux.HandleFunc("GET /status", httpBoundary(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, n.readStatus())
	}))

	mux.HandleFunc("GET /topology", httpBoundary(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, topology(n.readStatus(), n.publicRegistry()))
	}))

	mux.HandleFunc("GET /config", httpBoundary(n.exportConfig))

	mux.HandleFunc("GET /metrics", httpBoundary(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.Header().Set("Cache-Control", "no-store")
		writeMetrics(w, n.readStatus(), &n.metrics, int(n.events.queued.Load()), time.Now())
	}))

	serveHTTP(throw2(net.Listen("tcp", address)), mux)
}
