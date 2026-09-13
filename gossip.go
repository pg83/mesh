package main

import (
	"net"
	"slices"
	"time"
)

const gossipBatchSize = 8

type EdgeRecords []Update
type VertexRecords []Vertex

func (n *Node) scanLocal(addresses InterfaceState) map[uint64]*LocalAddress {
	local := map[uint64]*LocalAddress{}
	incoming := map[SocketAddress]uint64{}

	for _, addr := range addresses {
		ip := net.IP(addr.ip.AsSlice())

		if !ip.IsLoopback() {
			id := n.remember(sourceVertex(n.cfg.Index, ip))

			local[id] = &LocalAddress{address: socketAddress(ip, 0), iface: addr.iface}
		}

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
	desired := map[Edge]bool{}
	me := n.reg.byIndex[n.cfg.Index].vertex().hash()

	for ep, local := range n.local {
		if local.receive {
			desired[Edge{From: ep, To: me}] = true
		}
	}

	for edge, channel := range n.channelStatus {
		if channel.Outgoing {
			if n.local[edge.From] != nil {
				desired[Edge{From: me, To: edge.From}] = true
			}
		} else {
			if n.local[edge.To] != nil {
				desired[Edge{From: edge.To, To: me}] = true
			}
		}
	}

	for edge, received := range n.observed {
		if now.Sub(received) >= sessionTimeout {
			delete(n.observed, edge)
			n.log.Info("link down", "from", n.addresses[edge.From].string(), "to", n.addresses[edge.To].string())
		} else {
			desired[edge] = true
		}
	}

	for edge, record := range n.graph {
		if record.Alive && !desired[edge] && (n.owned[edge] || edge.From == me || edge.To == me || n.excludesDial(n.addresses[edge.From], n.addresses[edge.To])) {
			n.record(edge, false)
		}
	}

	for edge := range desired {
		n.record(edge, true)
	}

	n.owned = desired
	n.recompute()
}

func (n *Node) advertisements() [][]byte {
	packets := [][]byte{}
	updates := EdgeRecords{}

	for edge, state := range n.graph {
		updates = append(updates, Update{Edge: edge, State: state})
	}

	slices.SortFunc(updates, func(a, b Update) int { return compareEdge(a.Edge, b.Edge) })

	vertices := VertexRecords{}
	seen := map[uint64]bool{}
	size := 3

	for _, update := range updates {
		for _, id := range []uint64{update.From, update.To} {
			if seen[id] {
				continue
			}

			vertex := n.addresses[id]
			length := len(appendVertex(nil, vertex))

			if size+length > 1000 && len(vertices) != 0 {
				packets = append(packets, encodeVertices(vertices))
				vertices = nil
				size = 3
			}

			vertices = append(vertices, vertex)
			seen[id] = true
			size += length
		}
	}

	if len(vertices) != 0 {
		packets = append(packets, encodeVertices(vertices))
	}

	for start := 0; start < len(updates); start += gossipBatchSize {
		packets = append(packets, encodeEdges(updates[start:min(start+gossipBatchSize, len(updates))]))
	}

	return packets
}

func (n *Node) handleEdges(updates EdgeRecords) {
	for _, update := range updates {
		if update.ID == 0 || n.addresses[update.From].hash() == 0 || n.addresses[update.To].hash() == 0 || update.From == update.To {
			continue
		}

		previous, exists := n.graph[update.Edge]

		if !exists || update.ID > previous.ID {
			n.graph[update.Edge] = update.State
		}
	}
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
