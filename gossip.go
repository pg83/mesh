package main

import (
	"crypto/ed25519"
	"encoding/json"
	"net"
	"slices"
	"time"
)

const gossipBatchSize = 8

type Ad struct {
	Index uint16   `json:"index"`
	Edges []Update `json:"edges"`
}

func encodeAd(blob, sig []byte) []byte {
	out := append([]byte{innerAd}, sig...)

	return append(out, blob...)
}

func (n *Node) scanLocal() map[Endpoint]*LocalEndpoint {
	local := map[Endpoint]*LocalEndpoint{}
	incoming := map[Endpoint]Endpoint{}

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

				if config.bind.IP != 0 && config.bind.IP != wire.IP {
					continue
				}

				public := config.public

				if public.IP == 0 {
					public.IP = wire.IP
				}

				if previous, exists := incoming[wire]; exists && previous != public {
					throwFmt("ambiguous endpoint binding: %s", wire.string())
				}

				if previous := local[public]; previous != nil && previous.address != wire {
					throwFmt("ambiguous public endpoint: %s", public.string())
				}

				incoming[wire] = public
				local[public] = &LocalEndpoint{socket: n.sockets[wire.Port], address: wire, iface: iface.Index}
			}
		}
	}

	n.incoming = incoming

	return local
}

func (n *Node) refresh(now time.Time) {
	n.local = n.scanLocal()

	desired := map[Edge]bool{}
	me := n.reg.byIndex[n.cfg.Index].endpoint()

	for ep := range n.local {
		desired[Edge{From: me, To: ep}] = true
		desired[Edge{From: ep, To: me}] = true
	}

	for edge, received := range n.observed {
		if now.Sub(received) >= sessionTimeout {
			delete(n.observed, edge)
			n.log.Info("link down", "from", edge.From.string(), "to", edge.To.string())
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

func (n *Node) publish() {
	updates := []Update{}

	for _, record := range n.graph {
		updates = append(updates, *record)
	}

	slices.SortFunc(updates, func(a, b Update) int { return compareEdge(a.Edge, b.Edge) })

	for start := 0; start < len(updates); start += gossipBatchSize {
		ad := Ad{Index: n.cfg.Index, Edges: updates[start:min(start+gossipBatchSize, len(updates))]}
		blob := throw2(json.Marshal(ad))
		inner := encodeAd(blob, ed25519.Sign(n.sig, blob))

		for index, session := range n.peers {
			for _, dst := range n.candidates(n.reg.byIndex[index]) {
				for src := range n.local {
					n.send(session.seal(inner, n.nextPacketID()), Edge{From: src, To: dst})
				}
			}
		}
	}
}

func (n *Node) handleAd(inner []byte) {
	if len(inner) < 1+ed25519.SignatureSize {
		return
	}

	sig, blob := inner[1:1+ed25519.SignatureSize], inner[1+ed25519.SignatureSize:]
	ad := Ad{}

	if json.Unmarshal(blob, &ad) != nil {
		return
	}

	peer := n.reg.byIndex[ad.Index]

	fresh := slices.ContainsFunc(ad.Edges, func(update Update) bool {
		previous := n.graph[update.Edge]

		return previous == nil || update.ID > previous.ID
	})

	if peer == nil || !fresh || !ed25519.Verify(peer.sig, blob, sig) {
		return
	}

	topologyChanged := false

	for _, update := range ad.Edges {
		if update.ID == 0 || update.From.IP == 0 || update.To.IP == 0 || update.From == update.To {
			continue
		}

		previous := n.graph[update.Edge]

		if previous != nil && update.ID <= previous.ID {
			continue
		}

		if previous == nil || previous.Alive != update.Alive {
			topologyChanged = true
		}

		n.graph[update.Edge] = &update
	}

	if topologyChanged {
		n.recompute()
	}
}

func (n *Node) candidates(peer *Peer) []Endpoint {
	addrs := []Endpoint{}

	addrs = append(addrs, peer.addresses...)

	for ep, index := range n.owners {
		if index == peer.index && ep.Port != 0 && !n.subnet.Contains(ep.ip()) {
			addrs = append(addrs, ep)
		}
	}

	for ep := range n.discovered[peer.index] {
		if !n.subnet.Contains(ep.ip()) {
			addrs = append(addrs, ep)
		}
	}

	slices.SortFunc(addrs, compareEndpoint)

	return slices.Compact(addrs)
}
