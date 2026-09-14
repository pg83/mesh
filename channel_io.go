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

func (a *Channel) post(message any) {
	select {
	case a.inbox.in <- message:
	case <-a.io.ctx.Done():
	}
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
