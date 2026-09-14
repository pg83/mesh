package main

import (
	"encoding/base64"
	"maps"
	"net"
	"slices"
)

type Peer struct {
	config    PeerConfig
	version   uint64
	packet    []byte
	session   *Session
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

func newPeer(pc PeerConfig, version uint64) *Peer {
	if pc.Index == 0 || pc.Index > 255 {
		throwFmt("index %d is outside 1..255", pc.Index)
	}

	if version == 0 {
		version = 1
	}

	p := &Peer{config: pc, version: version, index: pc.Index, pub: publicKey(pc.Pub), intip: parseIntip(pc.Intip), endpoints: pc.Endpoint}

	for _, config := range pc.Endpoint {
		config.validate()

		if ep := config.description(); ep.hash() != 0 {
			p.addresses = append(p.addresses, ep)
		}
	}

	p.packet = encodeRegistryRecord(p)

	if len(p.packet)+3 > registryPayloadSize {
		throwFmt("registry entry %d does not fit in one packet", pc.Index)
	}

	return p
}

func newRegistry(peers []PeerConfig, version uint64) *Registry {
	r := &Registry{
		byIndex: map[uint16]*Peer{},
		byIntip: map[[4]byte]*Peer{},
	}

	for _, pc := range peers {
		p := newPeer(pc, version)

		if _, dup := r.byIndex[p.index]; dup {
			throwFmt("duplicate index %d", p.index)
		}

		r.byIndex[p.index] = p
		r.byIntip[p.intip] = p
	}

	return r
}

func (r *Registry) copy() *Registry {
	return &Registry{byIndex: maps.Clone(r.byIndex), byIntip: maps.Clone(r.byIntip)}
}

func (p *Peer) publicConfig() PeerConfig {
	pc := PeerConfig{Name: p.config.Name, Index: p.index, Pub: p.config.Pub, Intip: p.config.Intip, Endpoint: []EndpointConfig{}}

	for _, ep := range p.addresses {
		pc.Endpoint = append(pc.Endpoint, EndpointConfig{Proto: ep.Proto, Addr: ep.Addr, Port: int(ep.Port), Path: ep.Path})
	}

	return pc
}

func (r *Registry) records() []RegistryRecord {
	records := []RegistryRecord{}

	for _, p := range r.byIndex {
		records = append(records, RegistryRecord{PeerConfig: p.publicConfig(), Version: p.version})
	}

	slices.SortFunc(records, func(a, b RegistryRecord) int { return int(a.Index) - int(b.Index) })

	return records
}
