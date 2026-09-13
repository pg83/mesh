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
	owners    map[uint64]uint16
	routes    map[uint64][]Edge
	channels  map[Edge]chan any
	enabled   map[Edge]bool
	gossip    [][]byte
}

type Received struct {
	packet []byte
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
	edge    Edge
	peer    uint16
	seen    time.Time
	status  *ChannelStatus
	dialing bool
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
	dial         <-chan DialResult
}

func (a *Channel) run() {
	ticker := time.NewTicker(tickInterval)

	defer ticker.Stop()

	for {
		var closed <-chan struct{}

		if a.io != nil {
			closed = a.io.ctx.Done()
		}

		select {
		case msg := <-a.inbox.out:
			switch v := msg.(type) {
			case *Snapshot:
				first := a.view == nil
				session := v.registry.byIndex[a.peer].session

				if session != a.session {
					a.closeTransport()
					a.session = session
					a.seen = time.Time{}
				}

				a.view = v
				a.updateTransport()

				if first {
					a.gossip()
					a.exchangeRegistry(time.Now())
				}
			case Received:
				a.receive(v)
			case Outbound:
				a.send(v.inner)
			case *ChannelIO:
				a.attachIO(v)
			}
		case result := <-a.dial:
			a.dial = nil

			for _, io := range result.channels {
				post(a.node.events.in, any(io))
			}

			a.report()
		case <-closed:
			a.io = nil
			a.seen = time.Time{}
			a.report()
		case <-ticker.C:
			a.report()

			a.gossip()
			a.exchangeRegistry(time.Now())
		}
	}
}

func (a *Channel) closeTransport() {
	if a.io != nil {
		a.io.stop()
		a.io = nil
		a.seen = time.Time{}
		a.report()
	}
}

func (a *Channel) gossip() {
	if a.outgoing && a.view != nil && a.enabled() {
		a.updateTransport()

		if a.io == nil {
			a.startDial()
		}

		if a.io != nil && a.io.source.Proto == "source" && a.io.target.Proto == "udp" {
			packet := bindingPacket(a.session, a.io.source, a.io.target, a.io.id)

			select {
			case a.io.queue.in <- packet:
			case <-a.io.ctx.Done():
			}
		}

		for _, inner := range a.view.gossip {
			a.send(inner)
		}
	}
}

func (a *Channel) report() {
	report := ChannelReport{session: a.session, edge: a.edge, peer: a.peer, seen: a.seen, dialing: a.dial != nil}

	if a.io != nil && a.io.ctx.Err() == nil {
		report.status = &ChannelStatus{Transport: a.io.transport(), Edge: a.edge, Outgoing: a.outgoing, ID: a.io.id}
	}

	post(a.node.events.in, any(report))
}

func (a *Channel) enabled() bool {
	return a.view != nil && a.view.local[a.edge.From] != nil && (a.view.enabled[a.edge] || (a.io != nil && a.io.write != nil))
}

func (a *Channel) updateTransport() {
	if a.view == nil || (a.outgoing && !a.enabled()) || (!a.outgoing && a.view.local[a.edge.To] == nil) {
		a.closeTransport()
	}
}

func (a *Channel) send(inner []byte) {
	if a.view == nil || !a.outgoing || !a.enabled() {
		return
	}

	a.packetID++

	packet := a.session.seal(inner, a.packetID)

	a.sendChannel(packet)
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

	inner, ok := a.session.open(r.packet)

	if !ok {
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
	}
}

func (a *Channel) edges(inner []byte) {
	updates, ok := decodeEdges(inner)

	if !ok {
		return
	}

	for _, u := range updates {
		if u.ID > a.view.graph[u.Edge].ID {
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
			if _, known := a.view.addresses[id]; !known {
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
			post(view.channels[edge], any(Outbound{inner: inner}))

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
