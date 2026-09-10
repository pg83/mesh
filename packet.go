package main

import "encoding/binary"

const (
	packetInit      = 1
	packetResponse  = 2
	packetTransport = 3
	innerKeepalive  = 0
	innerData       = 1
	innerAd         = 2
	headerInit      = 1 + 4
	headerResponse  = 1 + 4 + 4
	headerTransport = 1 + 4 + 8
	nonceSize       = 12
	maxPacket       = 65535
	maxHops         = 16
)

type Data struct {
	src    uint16
	path   []uint16
	cursor int
	ip     []byte
}

func encodeData(d *Data) []byte {
	out := make([]byte, 0, 1+2+1+2*len(d.path)+1+len(d.ip))

	out = append(out, innerData)
	out = binary.BigEndian.AppendUint16(out, d.src)
	out = append(out, byte(len(d.path)))

	for _, hop := range d.path {
		out = binary.BigEndian.AppendUint16(out, hop)
	}

	out = append(out, byte(d.cursor))
	out = append(out, d.ip...)

	return out
}

func decodeData(inner []byte) (*Data, bool) {
	if len(inner) < 4 {
		return nil, false
	}

	hops := int(inner[3])
	head := 4 + 2*hops + 1

	if hops == 0 || hops > maxHops || len(inner) < head {
		return nil, false
	}

	d := &Data{
		src:    binary.BigEndian.Uint16(inner[1:3]),
		path:   make([]uint16, hops),
		cursor: int(inner[head-1]),
		ip:     inner[head:],
	}

	for i := range d.path {
		d.path[i] = binary.BigEndian.Uint16(inner[4+2*i:])
	}

	if d.cursor >= hops {
		return nil, false
	}

	return d, true
}

func advanceCursor(inner []byte, cursor int) {
	inner[4+2*int(inner[3])] = byte(cursor)
}

func validIPv4(packet []byte) bool {
	if len(packet) < 20 || packet[0]>>4 != 4 {
		return false
	}

	head := int(packet[0]&15) * 4

	return head >= 20 && head <= len(packet) && int(binary.BigEndian.Uint16(packet[2:4])) == len(packet)
}
