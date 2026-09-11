package main

import (
	"net"
	"slices"
	"time"
)

const gossipBatchSize = 8

type Ad struct {
	Edges     []Update   `json:"edges"`
	Endpoints []Endpoint `json:"endpoints"`
}

func (n *Node) scanLocal() map[uint64]*LocalEndpoint {
	local := map[uint64]*LocalEndpoint{}
	incoming := map[Endpoint]uint64{}

	for _, iface := range throw2(net.Interfaces()) {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 || iface.Name == n.cfg.Tun {
			continue
		}

		for _, addr := range throw2(iface.Addrs()) {
			ip, _ := throw3(net.ParseCIDR(addr.String()))

			if ip.To4() == nil || ip.IsLinkLocalUnicast() || n.subnet.Contains(ip) {
				continue
			}

			for _, config := range n.endpoints {
				wire := endpoint(ip, int(config.bind.Port))

				if config.bind.Addr != "0.0.0.0" && config.bind.Addr != wire.Addr {
					continue
				}

				public := config.public

				if public.Addr == "0.0.0.0" {
					public.Addr = wire.Addr
				}

				if previous, exists := incoming[wire]; public.Proto == "udp" && exists && previous != public.hash() {
					throwFmt("ambiguous endpoint binding: %s", wire.string())
				}

				if previous := local[public.hash()]; previous != nil && previous.address != wire {
					throwFmt("ambiguous public endpoint: %s", public.string())
				}

				if public.Proto == "udp" {
					incoming[wire] = n.remember(public)
				} else {
					n.remember(public)
				}

				binding := &LocalEndpoint{address: wire, iface: iface.Index}

				if public.Proto == "udp" {
					binding.socket = n.sockets[wire.Port]
				}

				local[public.hash()] = binding
			}
		}
	}

	n.incoming = incoming

	return local
}

func (n *Node) refresh(now time.Time) {
	n.local = n.scanLocal()

	desired := map[Edge]bool{}
	me := n.reg.byIndex[n.cfg.Index].endpoint().hash()

	for ep := range n.local {
		desired[Edge{From: me, To: ep}] = true
		desired[Edge{From: ep, To: me}] = true
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
		if record.Alive && !desired[edge] && (n.owned[edge] || edge.From == me || edge.To == me) {
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
	updates := []Update{}

	for edge, state := range n.graph {
		updates = append(updates, Update{Edge: edge, State: state})
	}

	slices.SortFunc(updates, func(a, b Update) int { return compareEdge(a.Edge, b.Edge) })

	for start := 0; start < len(updates); {
		for size := min(gossipBatchSize, len(updates)-start); ; size-- {
			ad := Ad{Edges: updates[start : start+size]}
			seen := map[uint64]bool{}

			for _, u := range ad.Edges {
				for _, id := range []uint64{u.From, u.To} {
					if !seen[id] {
						ad.Endpoints = append(ad.Endpoints, n.addresses[id])
						seen[id] = true
					}
				}
			}

			packet := encodeAd(&ad)

			if len(packet) <= 1000 || size == 1 {
				packets = append(packets, packet)
				start += size

				break
			}
		}
	}

	return packets
}

func (n *Node) handleAd(ad *Ad) {
	for _, ep := range ad.Endpoints {
		n.remember(ep)
	}

	for _, update := range ad.Edges {
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
		if id := n.remember(ep); id != 0 {
			addrs = append(addrs, id)
		}
	}

	for id, index := range n.owners {
		ep := n.addresses[id]

		if index == peer.index && ep.Port != 0 && !n.subnet.Contains(ep.ip()) {
			addrs = append(addrs, id)
		}
	}

	for id := range n.discovered[peer.index] {
		if !n.subnet.Contains(n.addresses[id].ip()) {
			addrs = append(addrs, id)
		}
	}

	slices.Sort(addrs)

	return slices.Compact(addrs)
}
