package main

import (
	"net"
	"time"
)

const registryInterval = 5 * time.Second

type Snapshot struct {
	registry  *Registry
	graph     map[Edge]bool
	addresses map[uint32]Vertex
	local     map[uint32]*LocalAddress
	routes    map[uint32][]Edge
	hops      map[uint32][]uint16
	next      map[uint16]Edge
	channels  map[Edge]*Channel
	records   map[uint16]uint64
	vectors   map[uint16]*Vector
	exits     map[uint16]bool
	alive     map[uint16]bool
	gossip    []Advertisement
	bundles   [][]byte
}

type Advertisement struct {
	owner   uint16
	version uint64
	packet  []byte
}

type Received struct {
	packet []byte
	source uint32
	inner  []byte
	at     time.Time
	io     *ChannelIO
}

type Outbound struct{ inner []byte }

type TunPacket struct {
	payload     []byte
	destination net.IP
}

type ChannelReport struct {
	session *Session
	actor   *Channel
	edge    Edge
	peer    uint16
	seen    time.Time
	status  *ChannelStatus
}

type Channel struct {
	node         *Node
	edge         Edge
	peer         uint16
	outgoing     bool
	view         *Snapshot
	session      *Session
	replay       Replay
	started      bool
	since, seen  time.Time
	nextRegistry time.Time
	io           *ChannelIO
}

func (a *Channel) run() {
	defer func() { a.io.stop(); a.report() }()

	a.since = time.Now()

	if a.io.read != nil {
		go a.io.read(a.io.inbox.in)
	}

	if a.io.write != nil {
		go a.io.runWriter(a.node)
	}

	a.gossip()
	a.exchangeRegistry(a.since)

	for {
		select {
		case msg := <-a.io.inbox.out:
			switch v := msg.(type) {
			case *Snapshot:
				a.view = v

				if v.registry.byIndex[a.peer].session != a.session || (!a.outgoing && v.local[a.edge.To] == nil) || a.silent(time.Now()) {
					a.io.closeConnection()

					return
				}

				a.report()
				a.gossip()
				a.exchangeRegistry(time.Now())
			case Received:
				a.receive(v)
			case Outbound:
				a.send(kindData, v.inner)
			}
		case <-a.io.ctx.Done():
			return
		}
	}
}

func (a *Channel) silent(now time.Time) bool {
	last := a.seen

	if last.IsZero() {
		last = a.since
	}

	return !a.outgoing && now.Sub(last) >= sessionTimeout
}

func (a *Channel) gossip() {
	if !a.outgoing || a.view == nil || !a.enabled() {
		return
	}

	for _, inner := range a.view.bundles {
		a.send(kindVersions, inner)
	}

	held := a.view.vectors[a.peer]

	for _, ad := range a.view.gossip {
		if held == nil || held.Records[ad.owner] < ad.version {
			a.send(kindGraph, ad.packet)
		}
	}
}

func (a *Channel) report() {
	report := ChannelReport{session: a.session, edge: a.edge, peer: a.peer, seen: a.seen, actor: a}

	if a.io != nil && a.io.ctx.Err() == nil {
		report.status = &ChannelStatus{Transport: a.io.transport(), Edge: a.edge, Outgoing: a.outgoing, ID: a.io.id, Wire: a.io.wire}
	}

	post(a.node.events.in, any(report))
}

func (a *Channel) enabled() bool {
	return a.view != nil && a.io.ctx.Err() == nil
}

func (a *Channel) send(kind byte, inner []byte) {
	if a.view == nil || !a.outgoing || !a.enabled() {
		return
	}

	id := a.node.transportID.Add(1)

	if a.io.dialed && !a.started {
		id = a.io.id
	}

	a.started = true

	packet := a.session.seal(a.io.edge.From, kind, inner, id)

	a.node.metrics.sent[kind].Add(1)
	a.node.metrics.sentBytes.Add(uint64(len(packet)))

	select {
	case a.io.queue.in <- packet:
	case <-a.io.ctx.Done():
	}
}

func (a *Channel) receive(r Received) {
	if len(r.packet) < headerTransport {
		a.node.metrics.rejected[rejectShort].Add(1)

		return
	}

	if packetSender(r.packet) != a.peer {
		a.node.metrics.rejected[rejectHeader].Add(1)

		return
	}

	source, inner := r.source, r.inner

	if inner == nil {
		var ok bool
		source, inner, ok = a.session.open(r.packet)

		if !ok {
			a.node.metrics.rejected[rejectAuth].Add(1)

			return
		}
	}

	if source != a.edge.From || (r.io != nil && r.io != a.io) {
		a.node.metrics.rejected[rejectSource].Add(1)

		return
	}

	if !a.replay.accept(packetID(r.packet)) {
		a.node.metrics.rejected[rejectReplay].Add(1)

		return
	}

	kind := packetKind(r.packet)

	a.node.metrics.received[kind].Add(1)
	a.node.metrics.receivedBytes.Add(uint64(len(r.packet)))

	first := a.seen.IsZero() || r.at.Sub(a.seen) >= sessionTimeout

	if r.at.After(a.seen) {
		a.seen = r.at
	}

	if first {
		a.report()
	}

	switch kind {
	case kindGraph:
		a.graph(inner)
	case kindData:
		a.forward(inner)
	case kindRegistry:
		if records, ok := decodeRegistry(inner); ok {
			post(a.node.events.in, any(records))
		}
	case kindVersions:
		a.versions(inner)
	}
}

func (a *Channel) versions(inner []byte) {
	raw, ok := decompress(inner)
	chunks, valid := decodeBundle(raw)

	if !ok || !valid {
		a.node.metrics.vectorsInvalid.Add(1)

		return
	}

	for _, chunk := range chunks {
		if chunk.Owner == a.node.cfg.Index || a.view.registry.byIndex[chunk.Owner] == nil {
			continue
		}

		current := a.view.vectors[chunk.Owner]

		if current != nil && (chunk.Version < current.Version || (chunk.Version == current.Version && covered(current, chunk))) {
			a.node.metrics.vectorsStale.Add(1)

			continue
		}

		post(a.node.events.in, any(chunk))
	}
}

func covered(current, chunk *Vector) bool {
	for owner, version := range chunk.Records {
		if held, ok := current.Records[owner]; !ok || held != version {
			return false
		}
	}

	return true
}

func (a *Channel) graph(packed []byte) {
	inner, ok := decompress(packed)

	if !ok {
		a.node.metrics.recordsInvalid.Add(1)

		return
	}

	owner, version, ok := recordHead(inner)

	if !ok || owner == a.node.cfg.Index || a.view.registry.byIndex[owner] == nil || version <= a.view.records[owner] {
		a.node.metrics.recordsStale.Add(1)

		return
	}

	if record, ok := decodeRecord(owner, version, inner); ok {
		record.packet = packed
		post(a.node.events.in, any(record))
	} else {
		a.node.metrics.recordsInvalid.Add(1)
	}
}

func (a *Channel) forward(inner []byte) {
	d, ok := decodeData(inner)

	if !ok || d.hops[d.cursor] != a.node.cfg.Index || (d.cursor > 0 && d.hops[d.cursor-1] != a.peer) {
		return
	}

	d.cursor++
	advanceCursor(inner, d.cursor)
	a.node.routeData(a.view, d, inner)
}

func (a *Channel) post(message any) {
	select {
	case a.io.inbox.in <- message:
	case <-a.io.ctx.Done():
	}
}

func (a *Channel) exchangeRegistry(now time.Time) {
	if !a.outgoing || a.view == nil || !a.enabled() || now.Before(a.nextRegistry) {
		return
	}

	a.send(kindRegistry, a.view.registry.packet())
	a.nextRegistry = now.Add(registryInterval)
}
