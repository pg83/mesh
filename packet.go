package main

import "encoding/binary"

const (
	packetTransport = 3
	packetGraph     = 4
	packetRegistry  = 5
	innerData       = 1
	innerRegistry   = 4
	innerGraph      = 6
	nonceSize       = 24
	headerTransport = 1 + 2 + 8 + nonceSize
	maxPacket       = 65535
	maxHops         = 16
	maxRouteEdges   = 3 * maxHops
)

func validPacketType(kind byte) bool {
	return kind == packetTransport || kind == packetGraph || kind == packetRegistry
}

type Data struct {
	path    []Edge
	cursor  int
	payload []byte
}

func encodeData(d *Data) []byte {
	out := make([]byte, 0, 3+8*(len(d.path)+1)+len(d.payload))

	out = append(out, innerData, byte(len(d.path)), byte(d.cursor))
	out = binary.LittleEndian.AppendUint64(out, d.path[0].From)

	for _, edge := range d.path {
		out = binary.LittleEndian.AppendUint64(out, edge.To)
	}

	return append(out, d.payload...)
}

func decodeData(inner []byte) (*Data, bool) {
	if len(inner) < 3 {
		return nil, false
	}

	hops := int(inner[1])
	head := 3 + 8*(hops+1)

	if hops == 0 || hops > maxRouteEdges || len(inner) < head || int(inner[2]) >= hops {
		return nil, false
	}

	d := &Data{path: make([]Edge, hops), cursor: int(inner[2]), payload: inner[head:]}
	from := binary.LittleEndian.Uint64(inner[3:])

	for i := range d.path {
		to := binary.LittleEndian.Uint64(inner[11+8*i:])

		if from == 0 || to == 0 || from == to {
			return nil, false
		}

		d.path[i] = Edge{From: from, To: to}
		from = to
	}

	return d, true
}

func advanceCursor(inner []byte, cursor int) {
	inner[2] = byte(cursor)
}
