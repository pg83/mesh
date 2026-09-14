package main

import (
	"maps"
	"net"
	"net/netip"
	"slices"
	"time"
)

func (n *Node) scanLocal(addresses InterfaceState) map[uint64]*LocalAddress {
	local := map[uint64]*LocalAddress{}
	incoming := map[SocketAddress]uint64{}

	for _, addr := range addresses {
		ip := net.IP(addr.ip.AsSlice())

		for _, config := range n.bindings() {
			if ip.IsLoopback() && !config.bind.ip().IsLoopback() {
				continue
			}

			wire := socketAddress(ip, int(config.bind.Port))

			if config.bind.ipv6() != wire.ipv6() || (!config.bind.ip().IsUnspecified() && config.bind.Addr != wire.Addr) {
				continue
			}

			public := config.public

			if public.ip().IsUnspecified() {
				public.Addr = wire.Addr.String()
			}

			if previous, exists := incoming[wire]; public.Proto == "udp" && exists && previous != public.hash() {
				throwFmt("ambiguous endpoint binding: %s", wire.string())
			}

			if previous := local[public.hash()]; previous != nil && previous.address != wire {
				throwFmt("ambiguous public endpoint: %s", public.string())
			}

			if public.Proto == "udp" {
				incoming[wire] = n.remember(public.vertex())
			} else {
				n.remember(public.vertex())
			}

			binding := &LocalAddress{address: wire, iface: addr.iface, receive: true}

			local[public.hash()] = binding
		}
	}

	return local
}

func (n *Node) refresh(now time.Time) {
	n.syncLocal()

	for edge, received := range n.observed {
		if now.Sub(received) >= sessionTimeout {
			delete(n.observed, edge)
			n.metrics.linkDown.Add(1)
			n.log.Info("link down", "from", n.addresses[edge.From].string(), "to", n.addresses[edge.To].string())
		}
	}

	n.publishRecord()
	n.rebuild()
}

func (n *Node) advertisements() [][]byte {
	owners := slices.Sorted(maps.Keys(n.records))
	packets := make([][]byte, 0, len(owners))

	for _, owner := range owners {
		packets = append(packets, n.records[owner].packet)
	}

	return packets
}

type Candidate struct {
	id   uint64
	wire SocketAddress
}

func (n *Node) candidates(peer *Peer) []Candidate {
	addrs := []uint64{}

	for _, ep := range peer.addresses {
		addrs = append(addrs, n.remember(ep.vertex()))
	}

	for id, index := range n.owners {
		ep := n.addresses[id]

		if index == peer.index && ep.isEndpoint() && !n.subnet.Contains(ep.ip()) {
			addrs = append(addrs, id)
		}
	}

	slices.Sort(addrs)

	out := []Candidate{}

	for _, id := range slices.Compact(addrs) {
		out = append(out, Candidate{id: id, wire: n.wire(peer.index, id)})
	}

	return out
}

func (n *Node) wire(owner uint16, id uint64) SocketAddress {
	vertex := n.addresses[id]

	if vertex.Proto != "udp" {
		return SocketAddress{}
	}

	own := socketAddress(vertex.ip(), int(vertex.Port))
	seen := n.seen[SeenKey{owner: owner, listener: id}]

	if len(seen) == 0 || !own.Addr.IsPrivate() || n.onLink(own.Addr) {
		return own
	}

	return seen[0]
}

func (n *Node) onLink(addr netip.Addr) bool {
	for _, iface := range n.interfaces {
		if iface.prefix.Contains(addr) {
			return true
		}
	}

	return false
}
