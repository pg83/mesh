package main

import (
	"context"
	"encoding/binary"
	"time"
)

type DialResult struct{ channels []*ChannelIO }

type ChannelIO struct {
	session        *Session
	edge           Edge
	source, target Vertex
	peer           uint16
	outgoing       bool
	origin, id     uint64
	queue          *Mailbox[[]byte]
	ctx            context.Context
	cancel         context.CancelFunc
	write          func(context.Context, []byte)
	read           func(chan any)
}

func newChannelIO(session *Session, source, target Vertex, outgoing bool, origin, id uint64) *ChannelIO {
	ctx, cancel := context.WithCancel(context.Background())

	return &ChannelIO{session: session, edge: Edge{From: source.hash(), To: target.hash()}, source: source, target: target,
		peer: session.peer, outgoing: outgoing, origin: origin, id: id, queue: newMailbox[[]byte](ctx.Done()), ctx: ctx, cancel: cancel}
}

func (c *ChannelIO) stop() {
	c.cancel()
}

func (c *ChannelIO) transport() string {
	if c.source.isEndpoint() {
		return c.source.Proto
	}

	return c.target.Proto
}

func (a *Channel) sendChannel(packet []byte) {
	if a.io != nil {
		select {
		case a.io.queue.in <- packet:
		case <-a.io.ctx.Done():
		}

		return
	}

	a.startDial()
}

func (a *Channel) startDial() {
	if a.dial != nil || !a.view.enabled[a.edge] {
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

	go a.node.dialChannel(result, a.view, local, source, target, a.peer, first)
	a.report()
}

func (n *Node) dialChannel(result chan<- DialResult, view *Snapshot, local *LocalAddress, source, target Vertex, peer uint16, first []byte) {
	channels := []*ChannelIO{}

	defer func() { result <- DialResult{channels: channels} }()

	try(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

		defer cancel()

		session := view.registry.byIndex[peer].session
		id := binary.LittleEndian.Uint64(first[3:])

		if target.Proto == "udp" {
			io := newUDPChannel(session, local, source, target, id)

			if err := try(func() { io.write(ctx, first) }); err != nil {
				io.stop()
				throw(err)
			}

			channels = []*ChannelIO{io}

			return
		}

		socket := n.dialWebSocket(ctx, local, target.endpoint())
		accepted := false

		defer func() {
			if !accepted {
				socket.CloseNow()
			}
		}()

		writeWS(ctx, socket, first)

		packet := readWS(ctx, socket)
		remote, binding, ok := n.readBinding(packet, view)

		if !ok || !binding.Reply || remote != session || binding.From.hash() != target.hash() || binding.To.hash() != source.hash() {
			return
		}

		ws := newWSConnection(socket, session, source, target, source.hash(), id)

		channels = []*ChannelIO{ws.send, ws.receive}
		accepted = true
	}).catch(func(e *Exception) { n.log.Debug("channel dial failed", "endpoint", target.string(), "err", e) })
}

func (a *Channel) attachIO(c *ChannelIO) {
	old := a.io
	local := a.edge.To

	if a.outgoing {
		local = a.edge.From
	}

	if a.view == nil || c.session != a.session || c.ctx.Err() != nil || a.view.local[local] == nil ||
		(old != nil && old.ctx.Err() == nil && (old.origin < c.origin || (old.origin == c.origin && old.id >= c.id))) {
		c.stop()

		return
	}

	if old != nil {
		old.stop()
	}

	a.io = c
	a.seen = time.Time{}

	if c.read != nil {
		go c.read(a.inbox.in)
	}

	if c.write != nil {
		go c.runWriter(a.node)
	}

	a.report()
}

func (c *ChannelIO) runWriter(n *Node) {
	defer c.stop()

	try(func() {
		for {
			select {
			case packet := <-c.queue.out:
				c.write(c.ctx, packet)
			case <-c.ctx.Done():
				return
			}
		}
	}).catch(func(e *Exception) { n.log.Debug("channel write failed", "err", e) })
}
