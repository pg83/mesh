package main

import (
	"encoding/binary"
	"net"
)

const (
	recordHeader     = 11
	recordExit       = 1
	recordVertexHead = 4
	recordLinkSize   = 7
)

func appendCounter(out []byte, id uint32) []byte {
	return append(out, byte(id), byte(id>>8), byte(id>>16))
}

func readCounter(data []byte) uint32 {
	return uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16
}

func appendVertex(out []byte, ep Vertex) []byte {
	var kind byte

	switch ep.Proto {
	case "udp":
		kind = 1

		if ep.ipv6() {
			kind = 4
		}

	case "tcp":
		kind = 5

		if ep.ipv6() {
			kind = 6
		}
	case "ws":
		kind = 2
	case "wss":
		kind = 3
	}

	flag := kind

	if ep.Endpoint {
		flag |= 128
	}

	out = append(out, flag)
	out = binary.LittleEndian.AppendUint16(out, ep.Port)

	if kind != 2 && kind != 3 {
		ip := ep.ip().To16()

		if kind == 1 || kind == 5 {
			ip = ip.To4()
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

func decodeVertex(data []byte) (Vertex, []byte, bool) {
	if len(data) < 7 {
		return Vertex{}, nil, false
	}

	ep := Vertex{Port: binary.LittleEndian.Uint16(data[1:3]), Endpoint: data[0]&128 != 0}
	kind := data[0] & 127

	data = data[3:]

	switch kind {
	case 1, 4, 5, 6:
		size := 4

		if kind == 4 || kind == 6 {
			size = 16
		}

		if len(data) < size {
			return Vertex{}, nil, false
		}

		ep.Proto = "udp"

		if kind == 5 || kind == 6 {
			ep.Proto = "tcp"
		}

		ep.Addr = net.IP(data[:size]).String()

		return ep, data[size:], true
	case 2:
		ep.Proto = "ws"
	case 3:
		ep.Proto = "wss"
	default:
		return Vertex{}, nil, false
	}

	for _, field := range []*string{&ep.Addr, &ep.Path} {
		if len(data) < 2 {
			return Vertex{}, nil, false
		}

		size := int(binary.LittleEndian.Uint16(data))

		data = data[2:]

		if size > len(data) {
			return Vertex{}, nil, false
		}

		*field = string(data[:size])
		data = data[size:]
	}

	return ep, data, true
}

func encodeRecordBody(record *GraphRecord) []byte {
	out := binary.LittleEndian.AppendUint16(nil, uint16(len(record.Vertices)))

	for _, v := range record.Vertices {
		flags := byte(0)

		if v.Ingress {
			flags |= 1
		}

		if v.Egress {
			flags |= 2
		}

		out = appendVertex(append(appendCounter(out, v.ID), flags), v.Vertex)
	}

	out = binary.LittleEndian.AppendUint16(out, uint16(len(record.Links)))

	for _, link := range record.Links {
		out = binary.LittleEndian.AppendUint32(out, link.From)
		out = appendCounter(out, link.To)
	}

	out = binary.LittleEndian.AppendUint16(out, uint16(len(record.Observed)))

	for _, o := range record.Observed {
		out = binary.LittleEndian.AppendUint32(out, o.From)
		out = appendVertex(out, o.Seen)
	}

	return out
}

func encodeRecord(owner uint16, version uint64, flags byte, body []byte) []byte {
	out := make([]byte, 0, recordHeader+len(body))

	out = binary.LittleEndian.AppendUint16(out, owner)
	out = binary.LittleEndian.AppendUint64(out, version)
	out = append(out, flags)

	return append(out, body...)
}

func recordHead(inner []byte) (uint16, uint64, bool) {
	if len(inner) < recordHeader+4 {
		return 0, 0, false
	}

	return binary.LittleEndian.Uint16(inner), binary.LittleEndian.Uint64(inner[2:]), true
}

func decodeRecord(owner uint16, version uint64, inner []byte) (*GraphRecord, bool) {
	record := &GraphRecord{Owner: owner, Version: version, Exit: inner[10]&recordExit != 0, Vertices: []RecordVertex{}, Links: []Edge{}, Observed: []Observation{}, packet: inner}
	data := inner[recordHeader:]

	if inner[10]&^recordExit != 0 {
		return nil, false
	}

	count := int(binary.LittleEndian.Uint16(data))

	data = data[2:]

	ids := map[uint32]bool{}

	for range count {
		if len(data) < recordVertexHead+7 {
			return nil, false
		}

		id := vertexID(owner, readCounter(data))
		flags := data[3]
		ep, rest, ok := decodeVertex(data[recordVertexHead:])

		if !ok || isHostID(id) || ids[id] || flags == 0 || flags > 3 || !ep.valid() || ep.isHost() {
			return nil, false
		}

		record.Vertices = append(record.Vertices, RecordVertex{ID: id, Vertex: ep.canonical(), Ingress: flags&1 != 0, Egress: flags&2 != 0})
		ids[id] = true
		data = rest
	}

	if len(data) < 2 {
		return nil, false
	}

	count = int(binary.LittleEndian.Uint16(data))
	data = data[2:]

	if len(data) < count*recordLinkSize+2 {
		return nil, false
	}

	sources := map[uint32]bool{}

	for range count {
		from := binary.LittleEndian.Uint32(data)
		to := vertexID(owner, readCounter(data[4:]))

		if isHostID(from) || !ids[to] || from == to {
			return nil, false
		}

		record.Links = append(record.Links, Edge{From: from, To: to})
		sources[from] = true
		data = data[recordLinkSize:]
	}

	count = int(binary.LittleEndian.Uint16(data))
	data = data[2:]

	for range count {
		if len(data) < 4+7 {
			return nil, false
		}

		from := binary.LittleEndian.Uint32(data)
		seen, rest, ok := decodeVertex(data[4:])

		if !ok || !sources[from] || seen.Proto != "udp" || seen.Port == 0 || !seen.valid() {
			return nil, false
		}

		record.Observed = append(record.Observed, Observation{From: from, Seen: seen.canonical()})
		data = rest
	}

	if len(data) != 0 {
		return nil, false
	}

	return record, true
}

func appendEndpoint(out []byte, ep Endpoint) []byte {
	return appendVertex(out, ep.vertex())
}

func decodeEndpoint(data []byte) (Endpoint, []byte, bool) {
	v, rest, ok := decodeVertex(data)

	return v.endpoint(), rest, ok && v.isEndpoint() && v.endpoint().valid()
}
