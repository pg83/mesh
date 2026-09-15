package main

import (
	"context"
	"github.com/coder/websocket"
	"time"
)

type WSConnection struct {
	send, receive *ChannelIO
	done          chan struct{}
}

func writeWS(ctx context.Context, conn *websocket.Conn, p []byte) {
	throw(conn.Write(ctx, websocket.MessageBinary, p))
}

func newWSConnection(socket *websocket.Conn, session *Session, edge Edge, source, target Vertex, listener bool, id uint64) *WSConnection {
	ctx, cancel := context.WithCancel(context.Background())

	c := &WSConnection{
		send:    newChannelIO(session, edge, source, target, true, listener, id),
		receive: newChannelIO(session, Edge{From: edge.To, To: edge.From}, target, source, false, listener, id),
		done:    make(chan struct{}),
	}

	c.send.sibling, c.receive.sibling = c.receive, c.send
	c.send.write = func(_ context.Context, p []byte) { writeWS(ctx, socket, p) }
	c.receive.read = func(input chan any) {
		defer c.receive.closeConnection()

		try(func() {
			for {
				kind, packet := throw3(socket.Read(ctx))

				if kind != websocket.MessageBinary {
					c.receive.stop()

					continue
				}

				if c.receive.ctx.Err() == nil {
					select {
					case input <- Received{packet: packet, at: time.Now(), io: c.receive}:
					case <-c.receive.ctx.Done():
					}
				}
			}
		})
	}

	go func() {
		<-c.send.ctx.Done()
		<-c.receive.ctx.Done()
		cancel()
		socket.CloseNow()
		close(c.done)
	}()

	return c
}
