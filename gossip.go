package main

import (
	"crypto/ed25519"
	"encoding/json"
	"net"
	"slices"
	"time"
)

const (
	adTimeout       = sessionTimeout
	gossipBatchSize = 6
)

type Ad struct {
	Index uint16   `json:"index"`
	Edges []Update `json:"edges"`
}

func encodeAd(blob, sig []byte) []byte {
	out := append([]byte{innerAd}, sig...)

	return append(out, blob...)
}

func (n *Node) scanLocal() map[Endpoint]int {
	local := map[Endpoint]int{}

	for _, iface := range throw2(net.Interfaces()) {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 || iface.Name == n.cfg.Tun {
			continue
		}

		for _, addr := range throw2(iface.Addrs()) {
			ip, _ := throw3(net.ParseCIDR(addr.String()))

			if ip.To4() == nil || ip.IsLinkLocalUnicast() || n.subnet.Contains(ip) {
				continue
			}

			local[endpoint(ip, n.cfg.Port)] = iface.Index
		}
	}

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

	for edge := range n.owned {
		if !desired[edge] {
			n.record(edge, false, now)
		}
	}

	for edge := range desired {
		n.record(edge, true, now)
	}

	n.owned = desired
	n.recompute(now)
}

func (n *Node) publish(now time.Time) {
	updates := []Update{}

	for _, record := range n.graph {
		remaining := record.expires.Sub(now)

		if remaining <= 0 {
			continue
		}

		update := record.Update

		update.TTL = uint32((remaining + time.Millisecond - 1) / time.Millisecond)
		updates = append(updates, update)
	}

	slices.SortFunc(updates, func(a, b Update) int { return compareEdge(a.Edge, b.Edge) })
	n.spread(updates, 0)
}

func (n *Node) spread(updates []Update, from uint16) {
	for start := 0; start < len(updates); start += gossipBatchSize {
		ad := Ad{Index: n.cfg.Index, Edges: updates[start:min(start+gossipBatchSize, len(updates))]}
		blob := throw2(json.Marshal(ad))
		inner := encodeAd(blob, ed25519.Sign(n.sig, blob))

		for index, session := range n.peers {
			if index == from {
				continue
			}

			for _, dst := range n.candidates(n.reg.byIndex[index]) {
				for src := range n.local {
					n.send(session.seal(inner, n.nextPacketID()), Edge{From: src, To: dst})
				}
			}
		}
	}
}

func (n *Node) handleAd(inner []byte, from uint16) {
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

	now := time.Now()
	changed := []Update{}
	topologyChanged := false

	for _, update := range ad.Edges {
		if update.ID == 0 || update.TTL == 0 || update.TTL > uint32(adTimeout/time.Millisecond) || update.From.IP == 0 || update.To.IP == 0 || update.From == update.To {
			continue
		}

		previous := n.graph[update.Edge]

		if previous != nil && update.ID <= previous.ID {
			continue
		}

		if previous == nil || previous.alive(now) != update.Alive {
			topologyChanged = true
		}

		n.graph[update.Edge] = &Record{Update: update, expires: now.Add(time.Duration(update.TTL) * time.Millisecond)}
		changed = append(changed, update)
	}

	if topologyChanged {
		n.recompute(now)
	}

	if len(changed) > 0 {
		n.spread(changed, from)
	}
}

func (n *Node) candidates(peer *Peer) []Endpoint {
	addrs := []Endpoint{}

	for _, addr := range peer.static {
		if ep := endpoint(addr.IP, addr.Port); ep.IP != 0 {
			addrs = append(addrs, ep)
		}
	}

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
