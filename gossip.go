package main

import (
	"maps"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"time"
)

func (n *Node) scanLocal(addresses InterfaceState) map[uint32]*LocalAddress {
	local := map[uint32]*LocalAddress{}
	incoming := map[SocketAddress]uint32{}

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

			id := n.listenerID(public.vertex())

			if previous, exists := incoming[wire]; public.Proto == "udp" && exists && previous != id {
				throwFmt("ambiguous endpoint binding: %s", wire.string())
			}

			if previous := local[id]; previous != nil && previous.address != wire {
				throwFmt("ambiguous public endpoint: %s", public.string())
			}

			if public.Proto == "udp" {
				incoming[wire] = id
			}

			n.addresses[id] = public.vertex().canonical()
			local[id] = &LocalAddress{address: wire, iface: addr.iface, receive: true}
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
			n.log.Info("link down", "from", n.describe(edge.From), "to", n.describe(edge.To))
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
	id     uint32
	vertex Vertex
	wire   SocketAddress
}

func (n *Node) candidates(peer *Peer) []Candidate {
	ids := map[uint32]Vertex{}

	for i, ep := range peer.addresses {
		ids[peer.endpointID(i)] = ep.vertex().canonical()
	}

	if record := n.records[peer.index]; record != nil {
		for _, v := range record.Vertices {
			if v.Ingress && v.isEndpoint() && !n.subnet.Contains(v.ip()) {
				ids[v.ID] = v.Vertex
			}
		}
	}

	out := []Candidate{}

	for _, id := range slices.Sorted(maps.Keys(ids)) {
		out = append(out, Candidate{id: id, vertex: ids[id], wire: n.wire(id, ids[id])})
	}

	return out
}

func (n *Node) wire(id uint32, vertex Vertex) SocketAddress {
	if vertex.Proto != "udp" {
		return SocketAddress{}
	}

	own := socketAddress(vertex.ip(), int(vertex.Port))
	seen := n.seen[id]

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

func (n *Node) describe(id uint32) string {
	if v, ok := n.addresses[id]; ok {
		return v.string()
	}

	return strconv.FormatUint(uint64(vertexOwner(id)), 10) + "/" + strconv.FormatUint(uint64(vertexCounter(id)), 10)
}
