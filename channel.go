package main

import (
	"encoding/binary"
	"time"
)

type Snapshot struct {
	registry  *Registry
	graph     map[Edge]bool
	addresses map[uint64]Vertex
	local     map[uint64]*LocalAddress
	owners    map[uint64]uint16
	routes    map[uint64][]Edge
	hops      map[uint64][]uint16
	next      map[uint16]Edge
	channels  map[Edge]*Channel
	records   map[uint16]uint64
	gossip    [][]byte
}

type Received struct {
	packet []byte
	source Vertex
	inner  []byte
	at     time.Time
	io     *ChannelIO
}

type Outbound struct{ inner []byte }
type TunPacket struct {
	payload     []byte
	destination uint64
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
	inbox        *Mailbox[any]
	view         *Snapshot
	session      *Session
	replay       Replay
	started      bool
	seen         time.Time
	nextRegistry time.Time
	io           *ChannelIO
}

func (a *Channel) run() {
	ticker := time.NewTicker(tickInterval)

	defer ticker.Stop()

	defer func() { a.io.stop(); a.report() }()

	for {
		select {
		case msg := <-a.inbox.out:
			switch v := msg.(type) {
			case *Snapshot:
				first := a.view == nil

				a.view = v

				if v.registry.byIndex[a.peer].session != a.session || (a.outgoing && !a.enabled()) || (!a.outgoing && v.local[a.edge.To] == nil) {
					return
				}

				if first {
					if a.io.read != nil {
						go a.io.read(a.inbox.in)
					}

					if a.io.write != nil {
						go a.io.runWriter(a.node)
					}

					a.gossip()
					a.exchangeRegistry(time.Now())
				}
			case Received:
				a.receive(v)
			case Outbound:
				a.send(v.inner)
			}
		case <-a.io.ctx.Done():
			return
		case now := <-ticker.C:
			if !a.outgoing && a.io.transport() == "udp" && !a.seen.IsZero() && now.Sub(a.seen) >= sessionTimeout {
				return
			}

			a.report()
			a.gossip()
			a.exchangeRegistry(time.Now())
		}
	}
}

func (a *Channel) gossip() {
	if a.outgoing && a.view != nil && a.enabled() {
		for _, inner := range a.view.gossip {
			a.send(inner)
		}
	}
}

func (a *Channel) report() {
	report := ChannelReport{session: a.session, edge: a.edge, peer: a.peer, seen: a.seen, actor: a}

	if a.io != nil && a.io.ctx.Err() == nil {
		report.status = &ChannelStatus{Transport: a.io.transport(), Edge: a.edge, Outgoing: a.outgoing, ID: a.io.id}
	}

	post(a.node.events.in, any(report))
}

func (a *Channel) enabled() bool {
	return a.view != nil && a.view.local[a.edge.From] != nil && a.io.ctx.Err() == nil
}

func (a *Channel) send(inner []byte) {
	if a.view == nil || !a.outgoing || !a.enabled() {
		return
	}

	id := a.node.transportID.Add(1)

	if a.io.dialed && !a.started {
		id = a.io.id
	}

	a.started = true

	packet := a.session.seal(a.io.source, inner, id)

	a.node.metrics.sent[packetKind(inner)].Add(1)
	a.node.metrics.sentBytes.Add(uint64(len(packet)))

	select {
	case a.io.queue.in <- packet:
	case <-a.io.ctx.Done():
	}
}

func (a *Channel) receive(r Received) {
	if a.outgoing || a.view == nil || a.view.local[a.edge.To] == nil {
		return
	}

	if len(r.packet) < headerTransport {
		a.node.metrics.rejected[rejectShort].Add(1)

		return
	}

	if binary.LittleEndian.Uint16(r.packet[1:]) != a.peer || !validPacketType(r.packet[0]) {
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

	if source.hash() != a.edge.From || (r.io != nil && r.io != a.io) {
		a.node.metrics.rejected[rejectSource].Add(1)

		return
	}

	if !a.replay.accept(binary.LittleEndian.Uint64(r.packet[3:])) {
		a.node.metrics.rejected[rejectReplay].Add(1)

		return
	}

	a.node.metrics.received[packetKind(inner)].Add(1)
	a.node.metrics.receivedBytes.Add(uint64(len(r.packet)))

	first := a.seen.IsZero() || r.at.Sub(a.seen) >= sessionTimeout

	if r.at.After(a.seen) {
		a.seen = r.at
	}

	if first {
		a.report()
	}

	switch inner[0] {
	case innerGraph:
		a.graph(inner)
	case innerData:
		a.forward(inner)
	case innerRegistry:
		if records, ok := decodeRegistry(inner); ok {
			post(a.node.events.in, any(records))
		}
	}
}

func (a *Channel) graph(inner []byte) {
	owner, version, ok := recordHead(inner)

	if !ok || owner == a.node.cfg.Index || a.view.registry.byIndex[owner] == nil || version <= a.view.records[owner] {
		a.node.metrics.recordsStale.Add(1)

		return
	}

	if record, ok := decodeRecord(owner, version, inner); ok {
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

func (n *Node) routeData(view *Snapshot, d *Data, inner []byte) {
	if d.cursor == len(d.hops) {
		n.metrics.tunDelivered.Add(1)

		if n.sshd != nil && n.sshd.accepts(d.payload) {
			n.sshd.inject(d.payload)
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
		packet := n.tun.read(buf)

		if destination := ipDestination(packet); destination != nil {
			post(n.tunInbox.in, any(TunPacket{payload: append([]byte(nil), packet...), destination: udpVertex(destination, 0).hash()}))
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
			hops := view.hops[v.destination]

			n.metrics.tunRead.Add(1)

			if len(hops) == 0 {
				n.metrics.tunUnrouted.Add(1)

				continue
			}

			d := &Data{hops: hops, payload: v.payload}

			n.routeData(view, d, encodeData(d))
		}
	}
}
