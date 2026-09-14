package main

import "encoding/binary"

const (
	packetTransport = 3
	packetEdges     = 4
	packetRegistry  = 5
	packetVertices  = 6
	packetMulticast = 7
	innerData       = 1
	innerEdges      = 2
	innerRegistry   = 4
	innerVertices   = 5
	innerMulticast  = 6
	nonceSize       = 24
	headerTransport = 1 + 2 + 8 + nonceSize
	maxPacket       = 65535
	maxHops         = 16
	maxRouteEdges   = 3 * maxHops
)

func validPacketType(kind byte) bool {
	return kind == packetTransport || kind == packetEdges || kind == packetRegistry || kind == packetVertices || kind == packetMulticast
}

type Data struct {
	path    []Edge
	cursor  int
	payload []byte
}

func encodeData(d *Data) []byte {
	out := make([]byte, 0, 3+16*len(d.path)+len(d.payload))

	out = append(out, innerData, byte(len(d.path)), byte(d.cursor))

	for _, edge := range d.path {
		out = binary.LittleEndian.AppendUint64(out, edge.From)
		out = binary.LittleEndian.AppendUint64(out, edge.To)
	}

	return append(out, d.payload...)
}

func decodeData(inner []byte) (*Data, bool) {
	if len(inner) < 3 {
		return nil, false
	}

	hops := int(inner[1])
	head := 3 + 16*hops

	if hops == 0 || hops > maxRouteEdges || len(inner) < head || int(inner[2]) >= hops {
		return nil, false
	}

	d := &Data{path: make([]Edge, hops), cursor: int(inner[2]), payload: inner[head:]}

	for i := range d.path {
		start := 3 + 16*i

		d.path[i] = Edge{From: binary.LittleEndian.Uint64(inner[start:]), To: binary.LittleEndian.Uint64(inner[start+8:])}

		if d.path[i].From == 0 || d.path[i].To == 0 || d.path[i].From == d.path[i].To || (i > 0 && d.path[i-1].To != d.path[i].From) {
			return nil, false
		}
	}

	return d, true
}

func advanceCursor(inner []byte, cursor int) {
	inner[2] = byte(cursor)
}
