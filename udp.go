package main

import (
	"context"
	"time"
)

func newUDPChannel(attempt *DialAttempt, id uint64) *ChannelIO {
	socket, local := attempt.socket, attempt.local
	writer := &UDPWriter{conn: socket.conn, local: local, remote: attempt.wire.udpAddr()}
	source := Vertex{Proto: "udp", Addr: attempt.source.Addr, Port: attempt.source.Port}
	c := newChannelIO(attempt.session, Edge{From: attempt.id, To: attempt.key.target}, source, attempt.target, true, false, id)

	c.local = &LocalAddress{address: local.address, iface: local.iface}
	c.wire = attempt.wire

	c.write = func(ctx context.Context, p []byte) {
		deadline, ok := ctx.Deadline()

		if !ok {
			deadline = time.Now().Add(time.Second)
		}

		throw(socket.conn.SetWriteDeadline(deadline))
		writer.writePacket(p)
	}

	return c
}

type UDPInputKey struct {
	remote, local SocketAddress
	peer          uint16
}

type UDPInput struct {
	channel *ChannelIO
	seen    time.Time
}
