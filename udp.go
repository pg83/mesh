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

func newUDPChannel(session *Session, local *LocalAddress, source, target Vertex, id uint64) *ChannelIO {
	config := net.ListenConfig{Control: udpControl(local.iface)}
	conn := throw2(config.ListenPacket(context.Background(), local.address.socketKey().network("udp"), net.JoinHostPort(local.address.ip().String(), "0"))).(*net.UDPConn)
	writer := &UDPWriter{conn: conn, local: local, remote: target.addr()}
	c := newChannelIO(session, source, target, true, source.hash(), id)

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

		if !known || input.channel.ctx.Err() != nil {
			view := n.currentSnapshot(context.Background())
			id := n.incomingID(view, key.local)
			session, binding, ok := n.readBinding(buf[:size], view)

			if !ok || binding.Reply || id == 0 || binding.To.hash() != id {
				continue
			}

			c := newChannelIO(session, binding.From, binding.To, false, binding.From.hash(), binary.LittleEndian.Uint64(buf[3:]))
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
		case input.input <- Received{packet: append([]byte(nil), buf[:size]...), at: now, io: input.channel}:
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
