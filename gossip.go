package main

import (
	"crypto/ed25519"
	"encoding/json"
	"net"
	"slices"
	"strconv"
	"time"
)

const (
	adInterval = 10 * time.Second
	adTimeout  = 40 * time.Second
)

type Ad struct {
	Index     uint16   `json:"index"`
	ID        uint64   `json:"ts"`
	Addrs     []string `json:"addrs"`
	Neighbors []uint16 `json:"neighbors"`
}

type Known struct {
	ad       *Ad
	blob     []byte
	sig      []byte
	received time.Time
}

func encodeAd(blob, sig []byte) []byte {
	out := make([]byte, 0, 1+ed25519.SignatureSize+len(blob))

	out = append(out, innerAd)
	out = append(out, sig...)

	return append(out, blob...)
}

func decodeAd(inner []byte) ([]byte, []byte, bool) {
	if len(inner) < 1+ed25519.SignatureSize {
		return nil, nil, false
	}

	return inner[1+ed25519.SignatureSize:], inner[1 : 1+ed25519.SignatureSize], true
}

func (n *Node) localAddrs() []string {
	port := strconv.Itoa(n.cfg.Port)
	addrs := []string{}

	for _, a := range n.reg.byIndex[n.cfg.Index].static {
		addrs = append(addrs, a.String())
	}

	for _, iface := range throw2(net.Interfaces()) {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 || iface.Name == n.cfg.Tun {
			continue
		}

		for _, a := range throw2(iface.Addrs()) {
			ip, _ := throw3(net.ParseCIDR(a.String()))

			if ip.To4() == nil || ip.IsLinkLocalUnicast() || n.subnet.Contains(ip) {
				continue
			}

			addrs = append(addrs, net.JoinHostPort(ip.String(), port))
		}
	}

	slices.Sort(addrs)

	return slices.Compact(addrs)
}

func (n *Node) neighbors() []uint16 {
	peers := make([]uint16, 0, len(n.sessions))

	for peer := range n.sessions {
		peers = append(peers, peer)
	}

	slices.Sort(peers)

	return peers
}

func (n *Node) publish(now time.Time) {
	ad := &Ad{
		Index:     n.cfg.Index,
		ID:        n.nextPacketID(),
		Addrs:     n.localAddrs(),
		Neighbors: n.neighbors(),
	}

	blob := throw2(json.Marshal(ad))

	known := &Known{
		ad:       ad,
		blob:     blob,
		sig:      ed25519.Sign(n.sig, blob),
		received: now,
	}

	n.ads[n.cfg.Index] = known
	n.lastAd = now
	n.flood(known, 0)
}

func (n *Node) flood(known *Known, from uint16) {
	inner := encodeAd(known.blob, known.sig)

	for peer := range n.peers {
		if peer != from {
			n.forward(peer, inner)
		}
	}
}

func (n *Node) handleAd(inner []byte, from uint16) {
	blob, sig, ok := decodeAd(inner)

	if !ok {
		return
	}

	ad := &Ad{}

	if json.Unmarshal(blob, ad) != nil {
		return
	}

	peer := n.reg.byIndex[ad.Index]

	if peer == nil || ad.Index == n.cfg.Index || !ed25519.Verify(peer.sig, blob, sig) {
		return
	}

	if old := n.ads[ad.Index]; old != nil && ad.ID <= old.ad.ID {
		return
	}

	n.ads[ad.Index] = &Known{ad: ad, blob: blob, sig: sig, received: time.Now()}
	n.recompute()
	n.flood(n.ads[ad.Index], from)
}

func (n *Node) syncTo(peer uint16) {
	for _, known := range n.ads {
		n.forward(peer, encodeAd(known.blob, known.sig))
	}
}

func (n *Node) expire(now time.Time) {
	for index, known := range n.ads {
		if index != n.cfg.Index && now.Sub(known.received) > adTimeout {
			delete(n.ads, index)
			n.recompute()
		}
	}
}
