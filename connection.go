package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"github.com/coder/websocket"
	"net"
	"time"
)

type PacketStream interface {
	read(context.Context) []byte
	write(context.Context, []byte)
	close()
}
type WSStream struct{ conn *websocket.Conn }

func (s *WSStream) read(ctx context.Context) []byte {
	return readWS(ctx, s.conn)
}

func (s *WSStream) write(ctx context.Context, p []byte) {
	throw(s.conn.Write(ctx, websocket.MessageBinary, p))
}

func (s *WSStream) close() {
	s.conn.CloseNow()
}

type UDPStream struct {
	conn   *net.UDPConn
	local  *LocalAddress
	buffer []byte
}

func (s *UDPStream) read(ctx context.Context) []byte {
	deadline, _ := ctx.Deadline()

	throw(s.conn.SetReadDeadline(deadline))

	if s.buffer == nil {
		s.buffer = make([]byte, maxPacket)
	}

	n := throw2(s.conn.Read(s.buffer))

	return append([]byte(nil), s.buffer[:n]...)
}

func (s *UDPStream) write(ctx context.Context, p []byte) {
	deadline, ok := ctx.Deadline()

	if !ok {
		deadline = time.Now().Add(time.Second)
	}

	throw(s.conn.SetWriteDeadline(deadline))
	s.writePacket(p)
}

func (s *UDPStream) close() {
	s.conn.Close()
}

func dialUDP(local *LocalAddress, remote Endpoint) PacketStream {
	dialer := net.Dialer{LocalAddr: &net.UDPAddr{IP: local.address.ip()}, Control: udpControl(local.iface)}
	conn := throw2(dialer.Dial(local.address.socketKey().network("udp"), remote.string())).(*net.UDPConn)

	throw(conn.SetReadBuffer(1 << 20))

	return &UDPStream{conn: conn, local: local}
}

func acceptUDP(local *LocalAddress, remote *net.UDPAddr) PacketStream {
	dialer := net.Dialer{LocalAddr: local.address.addr(), Control: udpControl(local.iface)}
	conn := throw2(dialer.Dial(local.address.socketKey().network("udp"), remote.String())).(*net.UDPConn)

	throw(conn.SetReadBuffer(1 << 20))

	return &UDPStream{conn: conn, local: local}
}

func (n *Node) discoverUDP(socket *UDPSocket) {
	buf := make([]byte, maxPacket)

	for {
		size, dst, addr, err := socket.read(buf)

		if errors.Is(err, net.ErrClosed) {
			return
		}

		throw(err)

		if size < headerTransport || dst == nil {
			continue
		}

		view := n.currentSnapshot(context.Background())
		id := n.incomingID(view, socketAddress(dst, int(socket.port)))
		session, binding, ok := n.readBinding(buf[:size], view)

		if !ok || binding.Reply || id == 0 || binding.To.hash() != id {
			continue
		}

		try(func() {
			stream := acceptUDP(view.local[id], addr.(*net.UDPAddr))
			accepted := false

			defer func() {
				if !accepted {
					stream.close()
				}
			}()

			stream.write(context.Background(), bindingReply(session, binding.To, binding.From, uint64(time.Now().UnixNano())))

			c := newConnection(stream, binding.To, binding.From, session.peer, binding.From.hash(), binary.LittleEndian.Uint64(buf[3:]))

			c.session = session
			post(n.events.in, any(c))
			accepted = true
		}).catch(func(e *Exception) { n.log.Debug("UDP accept failed", "err", e) })
	}
}

func (n *Node) incomingID(view *Snapshot, wire SocketAddress) uint64 {
	for id, local := range view.local {
		v := view.addresses[id]

		if v.isEndpoint() && v.Proto == "udp" && local.address == wire {
			return id
		}
	}

	return 0
}

type DialResult struct{ conn *Connection }
type Connection struct {
	session *Session
	conn    PacketStream
	edge    Edge
	source  Vertex
	target  Vertex
	peer    uint16
	origin  uint64
	id      uint64
	queue   *Mailbox[[]byte]
	ctx     context.Context
	cancel  context.CancelFunc
}
type Binding struct {
	From  Vertex `json:"from"`
	To    Vertex `json:"to"`
	Reply bool   `json:"reply,omitempty"`
}

func connectionKey(edge Edge) Edge {
	if edge.From > edge.To {
		return Edge{From: edge.To, To: edge.From}
	}

	return edge
}

func (n *Node) readBinding(packet []byte, view *Snapshot) (*Session, Binding, bool) {
	binding := Binding{}

	if len(packet) < headerTransport {
		return nil, binding, false
	}

	peer := binary.LittleEndian.Uint16(packet[1:])

	if peer == n.cfg.Index || view.registry.byIndex[peer] == nil {
		return nil, binding, false
	}

	session := view.registry.byIndex[peer].session
	inner, ok := session.open(packet)

	if !ok || len(inner) == 0 || inner[0] != innerBinding || json.Unmarshal(inner[1:], &binding) != nil {
		return nil, binding, false
	}

	binding.From = binding.From.canonical()
	binding.To = binding.To.canonical()

	if !binding.From.valid() || !binding.To.valid() || (binding.Reply && (binding.To.Proto != "source" || binding.To.Node != n.cfg.Index || !binding.From.isEndpoint())) || (!binding.Reply && (binding.From.Proto != "source" || binding.From.Node != peer || !binding.To.isEndpoint())) {
		return nil, binding, false
	}

	if owner := view.owners[binding.From.hash()]; owner != 0 && owner != peer {
		return nil, binding, false
	}

	return session, binding, true
}

func bindingPacket(session *Session, source, target Vertex, id uint64) []byte {
	blob := throw2(json.Marshal(Binding{From: source, To: target}))

	return session.seal(append([]byte{innerBinding}, blob...), id)
}

func newConnection(socket PacketStream, source, target Vertex, peer uint16, origin, id uint64) *Connection {
	ctx, cancel := context.WithCancel(context.Background())

	return &Connection{conn: socket, edge: Edge{From: source.hash(), To: target.hash()}, source: source, target: target,
		peer: peer, origin: origin, id: id, queue: newMailbox[[]byte](ctx.Done()), ctx: ctx, cancel: cancel}
}

func (c *Connection) stop() {
	c.cancel()
}

func (a *EdgeActor) sendConnection(packet []byte) {
	if a.connection != nil {
		select {
		case a.connection.queue.in <- packet:
		case <-a.connection.ctx.Done():
		}

		return
	}

	if a.dial != nil {
		return
	}

	local := a.view.local[a.edge.From]

	if local == nil {
		return
	}

	result := make(chan DialResult, 1)

	a.dial = result
	a.packetID++

	source, target := a.view.addresses[a.edge.From], a.view.addresses[a.edge.To]
	first := bindingPacket(a.session, source, target, a.packetID)
	view := a.view

	go a.node.dialConnection(result, view, local, source, target, a.peer, first)
	a.report()
}

func (n *Node) dialConnection(result chan<- DialResult, view *Snapshot, local *LocalAddress, source, target Vertex, peer uint16, first []byte) {
	var socket PacketStream
	var established *Connection

	defer func() {
		if established == nil && socket != nil {
			socket.close()
		}

		result <- DialResult{conn: established}
	}()

	try(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

		defer cancel()

		if target.Proto == "udp" {
			socket = dialUDP(local, target.endpoint())
			socket.write(ctx, first)
			established = newConnection(socket, source, target, peer, source.hash(), binary.LittleEndian.Uint64(first[3:]))
			established.session = view.registry.byIndex[peer].session

			return
		}

		socket = n.dialWebSocket(ctx, local, target.endpoint())
		socket.write(ctx, first)

		packet := socket.read(ctx)
		session, binding, ok := n.readBinding(packet, view)

		if !ok || !binding.Reply || session.peer != peer || binding.From.hash() != target.hash() || binding.To.hash() != source.hash() {
			return
		}

		established = newConnection(socket, source, target, peer, source.hash(), binary.LittleEndian.Uint64(first[3:]))
		established.session = session
	}).catch(func(e *Exception) { n.log.Debug("connection dial failed", "endpoint", target.string(), "err", e) })
}

func (a *EdgeActor) attachConnection(c *Connection) {
	old := a.connection

	if a.view == nil || c.session != a.session || a.view.local[a.edge.From] == nil || (old != nil && (old.origin < c.origin || (old.origin == c.origin && old.id >= c.id))) {
		c.stop()
		c.conn.close()

		return
	}

	if old != nil {
		old.stop()
	}

	a.connection = c

	input := a.view.actors[Edge{From: a.edge.To, To: a.edge.From}]

	go c.run(input, a.node)
	a.report()
}

func (c *Connection) run(input chan any, n *Node) {
	defer c.conn.close()

	defer c.stop()

	errors := make(chan *Exception, 2)

	go func() {
		errors <- try(func() {
			for {
				packet := c.conn.read(c.ctx)

				post(input, any(Received{packet: packet, at: time.Now(), connection: c}))
			}
		})
	}()

	go func() {
		errors <- try(func() {
			for {
				select {
				case packet := <-c.queue.out:
					c.conn.write(c.ctx, packet)
				case <-c.ctx.Done():
					return
				}
			}
		})
	}()

	select {
	case err := <-errors:
		err.catch(func(e *Exception) { n.log.Debug("connection stopped", "err", e) })
	case <-c.ctx.Done():
	}
}

func bindingReply(session *Session, source, target Vertex, id uint64) []byte {
	return session.seal(bindingInner(Binding{From: source, To: target, Reply: true}), id)
}

func bindingInner(binding Binding) []byte {
	return append([]byte{innerBinding}, throw2(json.Marshal(binding))...)
}

func (c *Connection) transport() string {
	if c.source.isEndpoint() {
		return c.source.Proto
	}

	return c.target.Proto
}
