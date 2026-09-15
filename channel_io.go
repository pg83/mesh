package main

import (
	"context"
)

type ChannelIO struct {
	session        *Session
	local          *LocalAddress
	edge           Edge
	source, target Vertex
	peer           uint16
	outgoing       bool
	listener       bool
	dialed         bool
	wire           SocketAddress
	origin, id     uint64
	queue          *Mailbox[[]byte]
	ctx            context.Context
	cancel         context.CancelFunc
	write          func(context.Context, []byte)
	read           func(chan any)

	sibling *ChannelIO
}

func newChannelIO(session *Session, edge Edge, source, target Vertex, outgoing, listener bool, id uint64) *ChannelIO {
	ctx, cancel := context.WithCancel(context.Background())

	return &ChannelIO{session: session, edge: edge, source: source, target: target, listener: listener,
		peer: session.peer, outgoing: outgoing, origin: uint64(edge.From), id: id, queue: newMailbox[[]byte](ctx.Done()), ctx: ctx, cancel: cancel}
}

func (c *ChannelIO) localID() uint32 {
	if c.outgoing {
		return c.edge.From
	}

	return c.edge.To
}

func (c *ChannelIO) stop() {
	c.cancel()
}

func (c *ChannelIO) closeConnection() {
	c.stop()

	if c.sibling != nil {
		c.sibling.stop()
	}
}

func (c *ChannelIO) transport() string {
	if c.source.isEndpoint() {
		return c.source.Proto
	}

	return c.target.Proto
}

func (a *Channel) post(message any) {
	select {
	case a.inbox.in <- message:
	case <-a.io.ctx.Done():
	}
}

func (c *ChannelIO) runWriter(n *Node) {
	defer c.closeConnection()

	try(func() {
		for {
			select {
			case packet := <-c.queue.out:
				c.write(c.ctx, packet)
			case <-c.ctx.Done():
				return
			}
		}
	}).catch(func(e *Exception) {
		n.log.Info("channel write failed", "from", c.source.string(), "to", c.target.string(), "err", e)
	})
}
