package main

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"time"
)

type UDPWriter struct {
	conn   *net.UDPConn
	local  *LocalAddress
	remote *net.UDPAddr
}

func newUDPChannel(session *Session, local *LocalAddress, target Vertex, id uint64) *ChannelIO {
	config := net.ListenConfig{Control: udpControl(local.iface)}
	conn := throw2(config.ListenPacket(context.Background(), local.address.socketKey().network("udp"), net.JoinHostPort(local.address.ip().String(), "0"))).(*net.UDPConn)
	source := socketVertex(conn.LocalAddr())
	writer := &UDPWriter{conn: conn, local: local, remote: target.addr()}
	c := newChannelIO(session, source, target, true, source.hash(), id)

	c.local = &LocalAddress{address: socketAddress(source.ip(), int(source.Port)), iface: local.iface}

	c.write = func(ctx context.Context, p []byte) {
		deadline, ok := ctx.Deadline()

		if !ok {
			deadline = time.Now().Add(time.Second)
		}

		throw(conn.SetWriteDeadline(deadline))
		writer.writePacket(p)
	}

	go func() { <-c.ctx.Done(); conn.Close() }()

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
		key := UDPInputKey{remote: socketAddress(remote.IP, remote.Port), local: socketAddress(dst, int(socket.port)), peer: binary.LittleEndian.Uint16(buf[1:])}
		input, known := inputs[key]
		now := time.Now()

		var source Vertex
		var inner []byte

		if known && input.channel.ctx.Err() == nil {
			var ok bool
			source, inner, ok = input.channel.session.open(buf[:size])

			if !ok {
				continue
			}

			known = source.hash() == input.channel.source.hash()
		}

		if !known || input.channel.ctx.Err() != nil {
			view := n.currentSnapshot()
			id := n.incomingID(view, key.local)
			session, from, body, ok := n.readPacket(buf[:size], view)

			source, inner = from, body

			if !ok || id == 0 || source.Proto != "udp" {
				continue
			}

			c := newChannelIO(session, source, view.addresses[id], false, source.hash(), binary.LittleEndian.Uint64(buf[3:]))

			c.local = view.local[id]

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

func (n *Node) incomingID(view *Snapshot, wire SocketAddress) uint64 {
	for id, local := range view.local {
		v := view.addresses[id]

		if v.isEndpoint() && v.Proto == "udp" && local.address == wire {
			return id
		}
	}

	return 0
}
