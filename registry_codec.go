package main

import (
	"encoding/binary"
	"math/rand/v2"
	"net"
)

const registryPayloadSize = 1200 - headerTransport - 16

type RegistryRecord struct {
	PeerConfig
	Version uint64 `json:"version"`
}

type RegistryRecords []RegistryRecord
type RegistryReader struct{ data []byte }

func appendRegistryString(out []byte, value string) []byte {
	if len(value) > 65535 {
		throwFmt("registry string too long")
	}

	out = binary.LittleEndian.AppendUint16(out, uint16(len(value)))

	return append(out, value...)
}

func encodeRegistryRecord(p *Peer) []byte {
	out := binary.LittleEndian.AppendUint16(nil, p.index)

	out = binary.LittleEndian.AppendUint64(out, p.version)
	out = append(out, p.intip[:]...)
	out = appendRegistryString(out, p.config.Pub)
	out = appendRegistryString(out, p.config.Name)
	out = binary.LittleEndian.AppendUint16(out, uint16(len(p.addresses)))

	for _, ep := range p.addresses {
		out = appendEndpoint(out, ep)
	}

	return out
}

func (r *Registry) packet() []byte {
	peers := make([]*Peer, 0, len(r.byIndex))

	for _, peer := range r.byIndex {
		peers = append(peers, peer)
	}

	out := []byte{innerRegistry, 0, 0}
	count := uint16(0)

	for _, i := range rand.Perm(len(peers)) {
		packet := peers[i].packet

		if len(out)+len(packet) <= registryPayloadSize {
			out = append(out, packet...)
			count++
		}
	}

	binary.LittleEndian.PutUint16(out[1:], count)

	return out
}

func (r *RegistryReader) take(size int) []byte {
	if size > len(r.data) {
		throwFmt("short registry record")
	}

	data := r.data[:size]

	r.data = r.data[size:]

	return data
}

func (r *RegistryReader) number() uint16 {
	return binary.LittleEndian.Uint16(r.take(2))
}

func (r *RegistryReader) text() string {
	return string(r.take(int(r.number())))
}

func decodeRegistry(inner []byte) (RegistryRecords, bool) {
	var records RegistryRecords

	err := try(func() {
		if len(inner) > registryPayloadSize {
			throwFmt("large registry packet")
		}

		r := &RegistryReader{data: inner}

		if r.take(1)[0] != innerRegistry {
			throwFmt("bad registry type")
		}

		count := int(r.number())

		for i := 0; i < count; i++ {
			index := r.number()
			version := binary.LittleEndian.Uint64(r.take(8))
			ip := net.IP(r.take(4)).String()
			pub, name := r.text(), r.text()
			peer := PeerConfig{Index: index, Pub: pub, Name: name, Intip: ip, Endpoint: []EndpointConfig{}}
			endpoints := int(r.number())

			if index == 0 || version == 0 {
				throwFmt("invalid registry identity or version")
			}

			for j := 0; j < endpoints; j++ {
				ep, rest, ok := decodeEndpoint(r.data)

				if !ok || !ep.valid() || ep.Port == 0 {
					throwFmt("bad registry endpoint")
				}

				r.data = rest
				peer.Endpoint = append(peer.Endpoint, EndpointConfig{Proto: ep.Proto, Addr: ep.Addr, Port: int(ep.Port), Path: ep.Path})
			}

			records = append(records, RegistryRecord{PeerConfig: peer, Version: version})
		}

		if len(r.data) != 0 {
			throwFmt("trailing registry data")
		}
	})

	return records, err == nil
}
