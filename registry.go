package main

import (
	"encoding/base64"
	"net"
)

type Peer struct {
	index     uint16
	pub       []byte
	intip     [4]byte
	endpoints []EndpointConfig
	addresses []Endpoint
}

type Registry struct {
	byIndex map[uint16]*Peer
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
		byIntip: map[[4]byte]*Peer{},
	}

	for _, pc := range peers {
		if pc.Index == 0 {
			throwFmt("index 0 is reserved")
		}

		p := &Peer{
			index: pc.Index,
			pub:   publicKey(pc.Pub),
			intip: parseIntip(pc.Intip),
		}

		p.endpoints = pc.Endpoint

		for _, config := range pc.Endpoint {
			config.validate()

			if ep := config.description(); ep.hash() != 0 {
				p.addresses = append(p.addresses, ep)
			}
		}

		if _, dup := r.byIndex[p.index]; dup {
			throwFmt("duplicate index %d", p.index)
		}

		r.byIndex[p.index] = p
		r.byIntip[p.intip] = p
	}

	return r
}
