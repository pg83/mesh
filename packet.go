package main

import "encoding/binary"

const (
	packetTransport = 3
	packetGossip    = 4
	innerData       = 1
	innerAd         = 2
	innerBinding    = 3
	nonceSize       = 24
	headerTransport = 1 + 2 + 8 + nonceSize
	maxPacket       = 65535
	maxHops         = 16
)

type Data struct {
	path   []Edge
	cursor int
	ip     []byte
}

func encodeData(d *Data) []byte {
	out := make([]byte, 0, 3+16*len(d.path)+len(d.ip))

	out = append(out, innerData, byte(len(d.path)), byte(d.cursor))

	for _, edge := range d.path {
		out = binary.LittleEndian.AppendUint64(out, edge.From)
		out = binary.LittleEndian.AppendUint64(out, edge.To)
	}

	return append(out, d.ip...)
}

func decodeData(inner []byte) (*Data, bool) {
	if len(inner) < 3 {
		return nil, false
	}

	hops := int(inner[1])
	head := 3 + 16*hops

	if hops == 0 || hops > maxHops || len(inner) < head || int(inner[2]) >= hops {
		return nil, false
	}

	d := &Data{path: make([]Edge, hops), cursor: int(inner[2]), ip: inner[head:]}

	for i := range d.path {
		start := 3 + 16*i

		d.path[i] = Edge{From: binary.LittleEndian.Uint64(inner[start:]), To: binary.LittleEndian.Uint64(inner[start+8:])}
	}

	return d, true
}

func advanceCursor(inner []byte, cursor int) {
	inner[2] = byte(cursor)
}

func validIPv4(packet []byte) bool {
	if len(packet) < 20 || packet[0]>>4 != 4 {
		return false
	}

	head := int(packet[0]&15) * 4

	return head >= 20 && head <= len(packet) && int(binary.BigEndian.Uint16(packet[2:4])) == len(packet)
}
