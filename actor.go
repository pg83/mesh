package main

import (
	"encoding/binary"
	"encoding/json"
	"net"
	"time"
)

type Snapshot struct {
	registry  *Registry
	graph     map[Edge]State
	addresses map[uint64]Vertex
	local     map[uint64]*LocalAddress
	owners    map[uint64]uint16
	routes    map[uint64][]Edge
	actors    map[Edge]chan any
	enabled   map[Edge]bool
	gossip    [][]byte
}

type Received struct {
	packet     []byte
	at         time.Time
	connection *Connection
}

type Outbound struct{ inner []byte }
type TunPacket struct{ ip []byte }

type EdgeReport struct {
	session    *Session
	edge       Edge
	peer       uint16
	seen       time.Time
	connection *ConnectionStatus
	dialing    bool
}

type EdgeActor struct {
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
	connection   *Connection
	dial         <-chan DialResult
}

func (a *EdgeActor) run() {
	ticker := time.NewTicker(tickInterval)

	defer ticker.Stop()

	for {
		var closed <-chan struct{}

		if a.connection != nil {
			closed = a.connection.ctx.Done()
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
			case *Connection:
				a.attachConnection(v)
			}
		case result := <-a.dial:
			a.dial = nil

			if result.conn != nil {
				a.attachConnection(result.conn)
			}

			a.report()
		case <-closed:
			a.connection = nil
			a.report()
		case <-ticker.C:
			a.report()

			a.gossip()
			a.exchangeRegistry(time.Now())
		}
	}
}

func (a *EdgeActor) closeTransport() {
	if a.connection != nil {
		a.connection.stop()
		a.connection = nil
	}
}

func (a *EdgeActor) gossip() {
	if a.outgoing && a.view != nil && a.enabled() {
		a.updateTransport()

		if a.connection != nil && a.connection.source.Proto == "source" && a.connection.target.Proto == "udp" {
			packet := bindingPacket(a.session, a.connection.source, a.connection.target, a.connection.id)

			post(a.connection.queue.in, packet)
		}

		for _, inner := range a.view.gossip {
			a.send(inner)
		}
	}
}

func (a *EdgeActor) report() {
	report := EdgeReport{session: a.session, edge: a.edge, peer: a.peer, seen: a.seen, dialing: a.dial != nil}

	if a.connection != nil {
		report.connection = &ConnectionStatus{Transport: a.connection.transport(), Edge: connectionKey(a.edge), Origin: a.connection.origin, ID: a.connection.id}
	}

	post(a.node.events.in, any(report))
}

func (a *EdgeActor) enabled() bool {
	return a.view != nil && a.view.local[a.edge.From] != nil && (a.view.enabled[a.edge] || a.connection != nil)
}

func (a *EdgeActor) updateTransport() {
	if a.outgoing && !a.enabled() {
		a.closeTransport()
	}
}

func (a *EdgeActor) send(inner []byte) {
	if a.view == nil || !a.outgoing || !a.enabled() {
		return
	}

	a.packetID++

	packet := a.session.seal(inner, a.packetID)

	a.sendConnection(packet)
}

func (a *EdgeActor) receive(r Received) {
	if a.outgoing || a.view == nil || a.view.local[a.edge.To] == nil || len(r.packet) < headerTransport {
		return
	}

	if r.connection != nil {
		select {
		case <-r.connection.ctx.Done():
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
	case innerBinding:
		var binding Binding

		if json.Unmarshal(inner[1:], &binding) == nil && !binding.Reply && binding.From.hash() == a.edge.From && binding.To.hash() == a.edge.To {
			post(a.view.actors[Edge{From: a.edge.To, To: a.edge.From}], any(Outbound{inner: bindingInner(Binding{From: binding.To, To: binding.From, Reply: true})}))
		}
	case innerAd:
		a.advertisement(inner)
	case innerData:
		a.forward(inner)
	case innerRegistry:
		if records, ok := decodeRegistry(inner); ok {
			post(a.node.events.in, any(records))
		}
	}
}

func (a *EdgeActor) advertisement(inner []byte) {
	ad, ok := decodeAd(inner)

	if !ok {
		return
	}

	fresh := false

	for _, u := range ad.Edges {
		if u.ID > a.view.graph[u.Edge].ID {
			fresh = true

			break
		}
	}

	if !fresh {
		return
	}

	post(a.node.events.in, any(ad))
}

func (a *EdgeActor) forward(inner []byte) {
	d, ok := decodeData(inner)

	if !ok || d.path[d.cursor] != a.edge {
		return
	}

	if d.cursor == len(d.path)-1 {
		if validIPv4(d.ip) && udpVertex(net.IP(d.ip[16:20]), 0).hash() == a.view.registry.byIndex[a.node.cfg.Index].vertex().hash() {
			post(a.node.tunWrites.in, d.ip)
		}

		return
	}

	d.cursor++
	advanceCursor(inner, d.cursor)
	post(a.view.actors[d.path[d.cursor]], any(Outbound{inner: inner}))
}

func (n *Node) readTun() {
	buf := make([]byte, maxPacket)

	for {
		packet := n.tun.read(buf)

		if validIPv4(packet) {
			post(n.tunInbox.in, any(TunPacket{ip: append([]byte(nil), packet...)}))
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

			path := view.routes[udpVertex(net.IP(v.ip[16:20]), 0).hash()]

			if len(path) != 0 {
				post(view.actors[path[0]], any(Outbound{inner: encodeData(&Data{path: path, ip: v.ip})}))
			}
		}
	}
}
