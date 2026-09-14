package main

import (
	"encoding/binary"
	"net"
)

const gossipEdgeSize = 25

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
	default:
		throwFmt("bad endpoint protocol %q", ep.Proto)
	}

	flag := kind

	if ep.Endpoint {
		flag |= 128
	}

	out = append(out, flag)
	out = binary.LittleEndian.AppendUint16(out, ep.Port)

	if kind == 1 || kind == 4 || kind == 5 || kind == 6 {
		ip := ep.ip().To16()

		if kind == 1 || kind == 5 {
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

func decodeVertex(data []byte) (Vertex, []byte, bool) {
	if len(data) < 7 {
		return Vertex{}, nil, false
	}

	ep := Vertex{Port: binary.LittleEndian.Uint16(data[1:3]), Endpoint: data[0]&128 != 0}
	kind := data[0] & 127

	data = data[3:]

	switch kind {
	case 1, 5:
		ep.Proto = "udp"

		if kind == 5 {
			ep.Proto = "tcp"
		}

		ep.Addr = net.IP(data[:4]).String()

		return ep, data[4:], true
	case 4, 6:
		if len(data) < 16 {
			return Vertex{}, nil, false
		}

		ep.Proto = "udp"

		if kind == 6 {
			ep.Proto = "tcp"
		}

		ep.Addr = net.IP(data[:16]).String()

		return ep, data[16:], true
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

func encodeEdges(edges EdgeRecords) []byte {
	if len(edges) > 65535 {
		throwFmt("too many edge records")
	}

	out := make([]byte, 1, 3+gossipEdgeSize*len(edges))

	out[0] = innerEdges
	out = binary.LittleEndian.AppendUint16(out, uint16(len(edges)))

	for _, update := range edges {
		out = binary.LittleEndian.AppendUint64(out, update.From)
		out = binary.LittleEndian.AppendUint64(out, update.To)
		out = binary.LittleEndian.AppendUint64(out, update.ID)

		alive := byte(0)

		if update.Alive {
			alive = 1
		}

		out = append(out, alive)
	}

	return out
}

func decodeEdges(inner []byte) (EdgeRecords, bool) {
	if len(inner) < 3 {
		return nil, false
	}

	count := int(binary.LittleEndian.Uint16(inner[1:3]))
	data := inner[3:]

	if count*gossipEdgeSize != len(data) {
		return nil, false
	}

	edges := make(EdgeRecords, count)

	for i := range edges {
		if data[24] > 1 {
			return nil, false
		}

		edges[i] = Update{Edge: Edge{From: binary.LittleEndian.Uint64(data), To: binary.LittleEndian.Uint64(data[8:])},
			State: State{ID: binary.LittleEndian.Uint64(data[16:]), Alive: data[24] == 1}}
		data = data[gossipEdgeSize:]
	}

	return edges, true
}

func encodeVertices(vertices VertexRecords) []byte {
	if len(vertices) > 65535 {
		throwFmt("too many vertex records")
	}

	out := binary.LittleEndian.AppendUint16([]byte{innerVertices}, uint16(len(vertices)))

	for _, vertex := range vertices {
		out = appendVertex(out, vertex)
	}

	return out
}

func decodeVertices(inner []byte) (VertexRecords, bool) {
	if len(inner) < 3 {
		return nil, false
	}

	count := int(binary.LittleEndian.Uint16(inner[1:3]))
	data := inner[3:]

	if count*7 > len(data) {
		return nil, false
	}

	vertices := make(VertexRecords, count)

	for i := range vertices {
		ep, rest, ok := decodeVertex(data)

		if !ok {
			return nil, false
		}

		vertices[i] = ep
		data = rest
	}

	return vertices, len(data) == 0
}

func appendEndpoint(out []byte, ep Endpoint) []byte {
	return appendVertex(out, ep.vertex())
}

func decodeEndpoint(data []byte) (Endpoint, []byte, bool) {
	v, rest, ok := decodeVertex(data)

	return v.endpoint(), rest, ok && v.isEndpoint() && v.endpoint().valid()
}
