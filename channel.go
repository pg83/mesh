package main

import (
	"encoding/binary"
	"time"
)

type Snapshot struct {
	registry  *Registry
	graph     map[Edge]State
	addresses map[uint64]Vertex
	local     map[uint64]*LocalAddress
	retired   map[uint64]bool
	owners    map[uint64]uint16
	routes    map[uint64][]Edge
	channels  map[Edge]*Channel
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
	packetID     uint64
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

	a.packetID++

	packet := a.session.seal(a.io.source, inner, a.packetID)

	select {
	case a.io.queue.in <- packet:
	case <-a.io.ctx.Done():
	}
}

func (a *Channel) receive(r Received) {
	if a.outgoing || a.view == nil || a.view.local[a.edge.To] == nil || len(r.packet) < headerTransport {
		return
	}

	if r.io != nil {
		select {
		case <-r.io.ctx.Done():
			return
		default:
		}
	}

	if binary.LittleEndian.Uint16(r.packet[1:]) != a.peer || !validPacketType(r.packet[0]) {
		return
	}

	source, inner := r.source, r.inner

	if inner == nil {
		var ok bool
		source, inner, ok = a.session.open(r.packet)

		if !ok {
			return
		}
	}

	if source.hash() != a.edge.From || (r.io != nil && r.io != a.io) {
		return
	}

	first := a.seen.IsZero() || r.at.Sub(a.seen) >= sessionTimeout

	if r.at.After(a.seen) {
		a.seen = r.at
	}

	if first {
		a.report()
	}

	if len(inner) == 0 {
		return
	}

	switch inner[0] {
	case innerEdges:
		a.edges(inner)
	case innerVertices:
		a.vertices(inner)
	case innerData:
		a.forward(inner)
	case innerRegistry:
		if records, ok := decodeRegistry(inner); ok {
			post(a.node.events.in, any(records))
		}
	case innerMulticast:
		if message, ok := decodeMulticast(inner); ok {
			post(a.node.multicast.in, any(message))
		}
	}
}

func (a *Channel) edges(inner []byte) {
	updates, ok := decodeEdges(inner)

	if !ok {
		return
	}

	me := a.view.registry.byIndex[a.node.cfg.Index].vertex().hash()

	for _, u := range updates {
		if u.From == me || u.To == me || u.ID > a.view.graph[u.Edge].ID ||
			a.view.retired[u.From] || a.view.retired[u.To] {
			post(a.node.events.in, any(updates))

			return
		}
	}
}

func (a *Channel) vertices(inner []byte) {
	vertices, ok := decodeVertices(inner)

	if !ok {
		return
	}

	for _, vertex := range vertices {
		if id := vertex.hash(); id != 0 {
			if old, known := a.view.addresses[id]; !known || old.Endpoint != vertex.Endpoint {
				post(a.node.events.in, any(vertices))

				return
			}
		}
	}
}

func (a *Channel) forward(inner []byte) {
	d, ok := decodeData(inner)

	if !ok || d.path[d.cursor] != a.edge {
		return
	}

	d.cursor++
	a.node.routeData(a.view, d, inner)
}

func (n *Node) routeData(view *Snapshot, d *Data, inner []byte) {
	me := view.registry.byIndex[n.cfg.Index].vertex().hash()
	local := func(id uint64) bool { return id == me || view.local[id] != nil }

	for d.cursor < len(d.path) {
		edge := d.path[d.cursor]

		if !local(edge.From) {
			return
		}

		if !local(edge.To) {
			advanceCursor(inner, d.cursor)

			if actor := view.channels[edge]; actor != nil {
				actor.post(Outbound{inner: inner})
			}

			return
		}

		if !view.graph[edge].Alive {
			return
		}

		d.cursor++
	}

	if d.path[len(d.path)-1].To == me {
		post(n.tunWrites.in, d.payload)
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
			if view == nil {
				continue
			}

			path := view.routes[v.destination]

			if len(path) != 0 {
				d := &Data{path: path, payload: v.payload}

				n.routeData(view, d, encodeData(d))
			}
		}
	}
}
