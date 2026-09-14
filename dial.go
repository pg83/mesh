package main

import (
	"context"
	"net"
	"net/netip"
	"time"
)

type DialKey struct {
	source InterfaceAddress
	target uint64
}

type DialAttempt struct {
	key      DialKey
	local    *LocalAddress
	target   Vertex
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

		for _, dst := range n.candidates(peer) {
			target := n.addresses[dst]

			for _, src := range n.interfaces {
				if src.ip.IsLoopback() || (target.ip() != nil && src.ip.Is6() != target.ipv6()) || n.excludesDial(Vertex{Addr: src.ip.String()}, target) {
					continue
				}

				key := DialKey{source: src, target: dst}

				desired[key] = true

				attempt := n.dials[key]

				if attempt != nil && attempt.session != peer.session {
					for _, c := range attempt.channels {
						c.stop()
					}

					attempt = nil
				}

				if attempt == nil {
					attempt = &DialAttempt{key: key, target: target, session: peer.session, local: &LocalAddress{address: socketAddress(net.IP(src.ip.AsSlice()), 0), iface: src.iface}}
					n.dials[key] = attempt
				}

				alive := false

				for _, c := range attempt.channels {
					alive = alive || c.ctx.Err() == nil
				}

				if !alive && !attempt.pending && !now.Before(attempt.next) {
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

	defer func() { post(n.events.in, any(result)) }()

	try(func() {
		id := uint64(time.Now().UnixNano())

		if attempt.target.Proto == "udp" {
			result.channels = []*ChannelIO{newUDPChannel(attempt.session, attempt.local, attempt.target, id)}

			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

		defer cancel()

		socket, address := n.dialWebSocket(ctx, attempt.local, attempt.target.endpoint())
		source := socketVertex(address)
		ws := newWSConnection(socket, attempt.session, source, attempt.target, source.hash(), id)
		local := &LocalAddress{address: socketAddress(address.IP, address.Port), iface: attempt.local.iface}

		ws.send.local, ws.receive.local = local, local
		result.channels = []*ChannelIO{ws.send, ws.receive}
	}).catch(func(e *Exception) { n.log.Debug("channel dial failed", "endpoint", attempt.target.string(), "err", e) })
}
