package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"net"
)

type Peer struct {
	index  uint16
	pub    []byte
	sig    ed25519.PublicKey
	intip  [4]byte
	static []*net.UDPAddr
}

type Registry struct {
	byIndex map[uint16]*Peer
	byPub   map[[32]byte]*Peer
	byIntip map[[4]byte]*Peer
}

func parseIntip(s string) [4]byte {
	ip := net.ParseIP(s).To4()

	if ip == nil {
		throwFmt("bad intip %q", s)
	}

	return [4]byte(ip)
}

func parseUDPAddr(s string) *net.UDPAddr {
	return throw2(net.ResolveUDPAddr("udp", s))
}

func decodeKey(s string) []byte {
	key := throw2(base64.StdEncoding.DecodeString(s))

	if len(key) != 32 {
		throwFmt("bad key length %d", len(key))
	}

	return key
}

func newRegistry(peers []PeerConfig) *Registry {
	r := &Registry{
		byIndex: map[uint16]*Peer{},
		byPub:   map[[32]byte]*Peer{},
		byIntip: map[[4]byte]*Peer{},
	}

	for _, pc := range peers {
		p := &Peer{
			index: pc.Index,
			pub:   decodeKey(pc.Pub),
			sig:   ed25519.PublicKey(decodeKey(pc.Sig)),
			intip: parseIntip(pc.Intip),
		}

		for _, s := range pc.Static {
			p.static = append(p.static, parseUDPAddr(s))
		}

		if _, dup := r.byIndex[p.index]; dup {
			throwFmt("duplicate index %d", p.index)
		}

		r.byIndex[p.index] = p
		r.byPub[[32]byte(p.pub)] = p
		r.byIntip[p.intip] = p
	}

	return r
}
