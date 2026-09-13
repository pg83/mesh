package main

import (
	"encoding/binary"
	"net"
)

const gossipEdgeSize = 25

func appendEndpoint(out []byte, ep Endpoint) []byte {
	var kind byte

	switch ep.Proto {
	case "udp":
		kind = 1

		if ep.ipv6() {
			kind = 4
		}
	case "ws":
		kind = 2
	case "wss":
		kind = 3
	default:
		throwFmt("bad endpoint protocol %q", ep.Proto)
	}

	out = append(out, kind)
	out = binary.LittleEndian.AppendUint16(out, ep.Port)

	if kind == 1 || kind == 4 {
		ip := ep.ip().To16()

		if kind == 1 {
			ip = ip.To4()
		}

		if ip == nil {
			throwFmt("bad UDP address %q", ep.Addr)
		}

		return append(out, ip...)
	}

	for _, value := range []string{ep.Addr, ep.Path} {
		if len(value) > 65535 {
			throwFmt("endpoint string too long")
		}

		out = binary.LittleEndian.AppendUint16(out, uint16(len(value)))
		out = append(out, value...)
	}

	return out
}

func decodeEndpoint(data []byte) (Endpoint, []byte, bool) {
	if len(data) < 7 {
		return Endpoint{}, nil, false
	}

	ep := Endpoint{Port: binary.LittleEndian.Uint16(data[1:3])}
	kind := data[0]

	data = data[3:]

	switch kind {
	case 1:
		ep.Proto = "udp"
		ep.Addr = net.IP(data[:4]).String()

		return ep, data[4:], true
	case 4:
		if len(data) < 16 {
			return Endpoint{}, nil, false
		}

		ep.Proto = "udp"
		ep.Addr = net.IP(data[:16]).String()

		return ep, data[16:], true
	case 2:
		ep.Proto = "ws"
	case 3:
		ep.Proto = "wss"
	default:
		return Endpoint{}, nil, false
	}

	for _, field := range []*string{&ep.Addr, &ep.Path} {
		if len(data) < 2 {
			return Endpoint{}, nil, false
		}

		size := int(binary.LittleEndian.Uint16(data))

		data = data[2:]

		if size > len(data) {
			return Endpoint{}, nil, false
		}

		*field = string(data[:size])
		data = data[size:]
	}

	return ep, data, true
}

func encodeAd(ad *Ad) []byte {
	if len(ad.Edges) > 65535 || len(ad.Endpoints) > 65535 {
		throwFmt("too many gossip records")
	}

	out := make([]byte, 1, 5+gossipEdgeSize*len(ad.Edges)+7*len(ad.Endpoints))

	out[0] = innerAd
	out = binary.LittleEndian.AppendUint16(out, uint16(len(ad.Edges)))
	out = binary.LittleEndian.AppendUint16(out, uint16(len(ad.Endpoints)))

	for _, update := range ad.Edges {
		out = binary.LittleEndian.AppendUint64(out, update.From)
		out = binary.LittleEndian.AppendUint64(out, update.To)
		out = binary.LittleEndian.AppendUint64(out, update.ID)

		alive := byte(0)

		if update.Alive {
			alive = 1
		}

		out = append(out, alive)
	}

	for _, ep := range ad.Endpoints {
		out = appendEndpoint(out, ep)
	}

	return out
}

func decodeAd(inner []byte) (*Ad, bool) {
	if len(inner) < 5 {
		return nil, false
	}

	edges := int(binary.LittleEndian.Uint16(inner[1:3]))
	endpoints := int(binary.LittleEndian.Uint16(inner[3:5]))
	data := inner[5:]

	if edges*gossipEdgeSize+endpoints*7 > len(data) {
		return nil, false
	}

	ad := &Ad{Edges: make([]Update, edges), Endpoints: make([]Endpoint, endpoints)}

	for i := range ad.Edges {
		if data[24] > 1 {
			return nil, false
		}

		ad.Edges[i] = Update{Edge: Edge{From: binary.LittleEndian.Uint64(data), To: binary.LittleEndian.Uint64(data[8:])},
			State: State{ID: binary.LittleEndian.Uint64(data[16:]), Alive: data[24] == 1}}
		data = data[gossipEdgeSize:]
	}

	for i := range ad.Endpoints {
		ep, rest, ok := decodeEndpoint(data)

		if !ok {
			return nil, false
		}

		ad.Endpoints[i] = ep
		data = rest
	}

	return ad, len(data) == 0
}
