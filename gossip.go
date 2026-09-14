package main

import (
	"maps"
	"net"
	"slices"
	"time"
)

func (n *Node) scanLocal(addresses InterfaceState) map[uint64]*LocalAddress {
	local := map[uint64]*LocalAddress{}
	incoming := map[SocketAddress]uint64{}

	for _, addr := range addresses {
		ip := net.IP(addr.ip.AsSlice())

		for _, config := range n.endpoints {
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

func (n *Node) candidates(peer *Peer) []uint64 {
	addrs := []uint64{}

	for _, ep := range peer.addresses {
		if id := n.remember(ep.vertex()); id != 0 {
			addrs = append(addrs, id)
		}
	}

	for id, index := range n.owners {
		ep := n.addresses[id]

		if index == peer.index && ep.isEndpoint() && !n.subnet.Contains(ep.ip()) {
			addrs = append(addrs, id)
		}
	}

	slices.Sort(addrs)

	return slices.Compact(addrs)
}
