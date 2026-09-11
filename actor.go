package main

import (
	"encoding/binary"
	"errors"
	"golang.org/x/net/ipv4"
	"net"
	"time"
)

type Snapshot struct {
	graph     map[Edge]State
	addresses map[uint64]Endpoint
	local     map[uint64]*LocalEndpoint
	owners    map[uint64]uint16
	routes    map[uint64][]Edge
	actors    map[Edge]chan any
	enabled   map[Edge]bool
	gossip    [][]byte
}

type Received struct {
	packet []byte
	at     time.Time
	udp    *UDPLink
	ws     *WSConnection
}

type Outbound struct{ inner []byte }
type TunPacket struct{ ip []byte }

type EdgeReport struct {
	edge       Edge
	peer       uint16
	seen       time.Time
	connection *WSStatus
	dialing    bool
}

type Discovery struct {
	wire     Endpoint
	remote   Endpoint
	peer     uint16
	received Received
}

type UDPLink struct {
	conn  *net.UDPConn
	local *LocalEndpoint
	done  chan struct{}
}

type EdgeActor struct {
	node     *Node
	edge     Edge
	peer     uint16
	outgoing bool
	inbox    *Mailbox[any]
	view     *Snapshot
	session  *Session
	packetID uint64
	seen     time.Time
	udp      *UDPLink
	ws       *WSConnection
	dial     <-chan DialResult
}

func (a *EdgeActor) run() {
	ticker := time.NewTicker(tickInterval)

	defer ticker.Stop()

	for {
		var closed <-chan struct{}

		if a.ws != nil {
			closed = a.ws.ctx.Done()
		}

		select {
		case msg := <-a.inbox.out:
			switch v := msg.(type) {
			case *Snapshot:
				first := a.view == nil

				a.view = v
				a.updateTransport()

				if first && a.udp != nil {
					a.gossip()
				}
			case Received:
				a.receive(v)
			case Outbound:
				a.send(v.inner)
			case *WSConnection:
				a.attachWS(v)
			}
		case result := <-a.dial:
			a.dial = nil

			if result.conn != nil {
				a.attachWS(result.conn)
			}

			a.report()
		case <-closed:
			a.ws = nil
			a.report()
		case <-ticker.C:
			a.report()

			a.gossip()
		}
	}
}

func (a *EdgeActor) gossip() {
	if a.outgoing && a.view != nil && a.view.enabled[a.edge] {
		a.updateTransport()

		for _, inner := range a.view.gossip {
			a.send(inner)
		}
	}
}

func (a *EdgeActor) report() {
	report := EdgeReport{edge: a.edge, peer: a.peer, seen: a.seen, dialing: a.dial != nil}

	if a.ws != nil {
		report.connection = &WSStatus{Edge: connectionKey(a.edge), Origin: a.ws.origin, ID: a.ws.id}
	}

	post(a.node.events.in, any(report))
}

func (a *EdgeActor) updateTransport() {
	if !a.outgoing {
		return
	}

	local := a.view.local[a.edge.From]

	if a.udp != nil && (local == nil || !a.view.enabled[a.edge] || local.address != a.udp.local.address || local.iface != a.udp.local.iface) {
		close(a.udp.done)
		a.udp.conn.Close()
		a.udp = nil
	}

	if local == nil || !a.view.enabled[a.edge] {
		if a.ws != nil {
			a.ws.stop()
			a.ws = nil
		}

		return
	}

	if local.socket != nil && a.udp == nil {
		try(func() {
			link := &UDPLink{conn: connectUDP(local, a.view.addresses[a.edge.To]), local: local, done: make(chan struct{})}

			a.udp = link

			input := a.view.actors[Edge{From: a.edge.To, To: a.edge.From}]

			go a.node.loop("edge UDP reader", func() { readEdgeUDP(link, input) })
		}).catch(func(e *Exception) { a.node.log.Debug("UDP connect failed", "err", e) })
	}
}

func readEdgeUDP(link *UDPLink, input chan any) {
	buf := make([]byte, maxPacket)

	for {
		size, err := link.conn.Read(buf)

		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}

			continue
		}

		post(input, any(Received{packet: append([]byte(nil), buf[:size]...), at: time.Now(), udp: link}))
	}
}

func (a *EdgeActor) send(inner []byte) {
	if a.view == nil || !a.outgoing || !a.view.enabled[a.edge] {
		return
	}

	a.packetID++

	packet := a.session.seal(inner, a.packetID)

	if a.view.local[a.edge.From].socket == nil {
		a.sendWS(packet)
	} else if a.udp != nil {
		a.udp.conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))

		control := &ipv4.ControlMessage{Src: a.udp.local.address.ip(), IfIndex: a.udp.local.iface}

		a.udp.conn.WriteMsgUDP(packet, control.Marshal(), nil)
	}
}

func (a *EdgeActor) receive(r Received) {
	if a.outgoing || a.view == nil || a.view.local[a.edge.To] == nil || len(r.packet) < headerTransport {
		return
	}

	if r.udp != nil {
		select {
		case <-r.udp.done:
			return
		default:
		}
	}

	if r.ws != nil {
		select {
		case <-r.ws.ctx.Done():
			return
		default:
		}
	}

	if binary.LittleEndian.Uint16(r.packet[1:]) != a.peer || (r.packet[0] != packetTransport && r.packet[0] != packetGossip) {
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
	case innerAd:
		a.advertisement(inner)
	case innerData:
		a.forward(inner)
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
		if validIPv4(d.ip) && endpoint(net.IP(d.ip[16:20]), 0).hash() == a.node.reg.byIndex[a.node.cfg.Index].endpoint().hash() {
			post(a.node.tunWrites.in, d.ip)
		}

		return
	}

	d.cursor++
	advanceCursor(inner, d.cursor)
	post(a.view.actors[d.path[d.cursor]], any(Outbound{inner: inner}))
}

func (n *Node) discoverUDP(socket *UDPSocket) {
	buf := make([]byte, maxPacket)
	sessions := map[uint16]*Session{}

	for {
		size, control, addr, err := socket.conn.ReadFrom(buf)

		throw(err)

		if size < headerTransport || control == nil || control.Dst == nil || (buf[0] != packetTransport && buf[0] != packetGossip) {
			continue
		}

		peer := binary.LittleEndian.Uint16(buf[1:])

		if peer == n.cfg.Index || n.reg.byIndex[peer] == nil {
			continue
		}

		s := sessions[peer]

		if s == nil {
			s = n.session(peer)
			sessions[peer] = s
		}

		if _, ok := s.open(buf[:size]); !ok {
			continue
		}

		remote := addr.(*net.UDPAddr)

		post(n.events.in, any(Discovery{wire: endpoint(control.Dst, int(socket.port)), remote: endpoint(remote.IP, remote.Port), peer: peer,
			received: Received{packet: append([]byte(nil), buf[:size]...), at: time.Now()}}))
	}
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

			path := view.routes[endpoint(net.IP(v.ip[16:20]), 0).hash()]

			if len(path) != 0 {
				post(view.actors[path[0]], any(Outbound{inner: encodeData(&Data{path: path, ip: v.ip})}))
			}
		}
	}
}
