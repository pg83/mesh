package main

import (
	"context"
	"errors"
	"net"
	"time"
)

type UDPWriter struct {
	conn   *net.UDPConn
	local  *LocalAddress
	remote *net.UDPAddr
}

func newUDPChannel(attempt *DialAttempt, id uint64) *ChannelIO {
	socket, local := attempt.socket, attempt.local
	writer := &UDPWriter{conn: socket.conn, local: local, remote: attempt.wire.udpAddr()}
	c := newChannelIO(attempt.session, Edge{From: attempt.id, To: attempt.key.target}, attempt.source.canonical(), attempt.target, true, false, id)

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
	input   chan any
	seen    time.Time
}

func (n *Node) discoverUDP(socket *UDPSocket) {
	buf := make([]byte, maxPacket)
	inputs := map[UDPInputKey]UDPInput{}

	defer func() {
		for _, input := range inputs {
			input.channel.stop()
		}
	}()

	for {
		size, dst, addr, err := socket.read(buf)

		if errors.Is(err, net.ErrClosed) {
			return
		}

		throw(err)

		if size < headerTransport || dst == nil {
			continue
		}

		remote := addr.(*net.UDPAddr)
		key := UDPInputKey{remote: socketAddress(remote.IP, remote.Port), local: socketAddress(dst, int(socket.port)), peer: packetSender(buf)}
		input, known := inputs[key]
		now := time.Now()

		var source uint32
		var inner []byte

		if known && input.channel.ctx.Err() == nil {
			var ok bool
			source, inner, ok = input.channel.session.open(buf[:size])

			if !ok {
				continue
			}

			known = source == input.channel.edge.From
		}

		if !known || input.channel.ctx.Err() != nil {
			view := n.currentSnapshot()
			id := n.incomingID(view, key.local)
			session, from, body, ok := n.readPacket(buf[:size], view)

			if !ok || id == 0 {
				continue
			}

			source, inner = from, body

			c := newChannelIO(session, Edge{From: source, To: id}, Vertex{}, view.addresses[id], false, true, packetID(buf))

			c.local = view.local[id]
			c.wire = key.remote

			ready := make(chan chan any, 1)

			c.read = func(in chan any) { ready <- in }
			post(n.events.in, any(c))

			select {
			case input.input = <-ready:
			case <-c.ctx.Done():
				continue
			}

			input.channel = c

			for key, old := range inputs {
				if now.Sub(old.seen) >= sessionTimeout {
					old.channel.stop()
					delete(inputs, key)
				}
			}
		}

		input.seen = now
		inputs[key] = input

		select {
		case input.input <- Received{packet: append([]byte(nil), buf[:headerTransport]...), source: source, inner: inner, at: now, io: input.channel}:
		case <-input.channel.ctx.Done():
		}
	}
}

func (n *Node) incomingID(view *Snapshot, wire SocketAddress) uint32 {
	for id, local := range view.local {
		v := view.addresses[id]

		if v.isEndpoint() && v.Proto == "udp" && local.address == wire {
			return id
		}
	}

	return 0
}
