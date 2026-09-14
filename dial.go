package main

import (
	"context"
	"net"
	"net/netip"
	"time"
)

type DialKey struct {
	source InterfaceAddress
	target uint32
	vertex Vertex
	wire   SocketAddress
}

type DialAttempt struct {
	key      DialKey
	id       uint32
	local    *LocalAddress
	target   Vertex
	wire     SocketAddress
	source   Vertex
	socket   *UDPSocket
	session  *Session
	channels []*ChannelIO
	pending  bool
	next     time.Time
}

type DialResult struct {
	attempt  *DialAttempt
	channels []*ChannelIO
}

func (n *Node) excludesDial(from, to Vertex) bool {
	src, _ := netip.ParseAddr(from.Addr)
	dst, _ := netip.ParseAddr(to.Addr)

	return n.noDial[DialPair{From: src.Unmap(), To: dst.Unmap()}]
}

func (n *Node) syncDials() {
	desired := map[DialKey]bool{}
	now := time.Now()

	for index, peer := range n.reg.byIndex {
		if index == n.cfg.Index {
			continue
		}

		for _, candidate := range n.candidates(peer) {
			dst, wire := candidate.id, candidate.wire
			target := candidate.vertex
			remote := target

			if target.Proto == "udp" {
				remote = wire.vertex()
			}

			for _, src := range n.interfaces {
				if src.ip.IsLoopback() || (remote.ip() != nil && src.ip.Is6() != remote.ipv6()) || n.excludesDial(Vertex{Addr: src.ip.String()}, remote) {
					continue
				}

				key := DialKey{source: src, target: dst, vertex: target, wire: wire}

				desired[key] = true

				attempt := n.dials[key]

				if attempt != nil && attempt.session != peer.session {
					for _, c := range attempt.channels {
						c.stop()
					}

					attempt = nil
				}

				if attempt == nil {
					attempt = &DialAttempt{key: key, id: n.allocate(), target: target, wire: wire, session: peer.session, local: &LocalAddress{address: socketAddress(net.IP(src.ip.AsSlice()), 0), iface: src.iface}}
					n.dials[key] = attempt
				}

				alive := false

				for _, c := range attempt.channels {
					alive = alive || c.ctx.Err() == nil
				}

				if !alive && !attempt.pending && !now.Before(attempt.next) {
					if target.Proto == "udp" {
						source := n.udpSource(src)

						if source == nil {
							continue
						}

						attempt.socket, attempt.source, attempt.local = source.socket, source.vertex, source.local
					}

					attempt.pending = true
					attempt.next = now.Add(time.Second)
					go n.dialChannel(attempt)
				}
			}
		}
	}

	for key, attempt := range n.dials {
		if !desired[key] {
			for _, c := range attempt.channels {
				c.stop()
			}

			delete(n.dials, key)
		}
	}
}

func (n *Node) dialChannel(attempt *DialAttempt) {
	result := DialResult{attempt: attempt}

	defer func() {
		for _, c := range result.channels {
			c.dialed = true
		}

		post(n.events.in, any(result))
	}()

	try(func() {
		id := n.transportID.Add(1)

		if attempt.target.Proto == "udp" {
			result.channels = []*ChannelIO{newUDPChannel(attempt, id)}

			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

		defer cancel()

		socket, address := n.dialWebSocket(ctx, attempt.local, attempt.target.endpoint())
		source := socketVertex(address)
		ws := newWSConnection(socket, attempt.session, Edge{From: attempt.id, To: attempt.key.target}, source, attempt.target, false, id)
		local := &LocalAddress{address: socketAddress(address.IP, address.Port), iface: attempt.local.iface}

		ws.send.local, ws.receive.local = local, local
		result.channels = []*ChannelIO{ws.send, ws.receive}
	}).catch(func(e *Exception) {
		n.metrics.dialFailed.Add(1)
		n.log.Debug("channel dial failed", "endpoint", attempt.target.string(), "err", e)
	})
}
