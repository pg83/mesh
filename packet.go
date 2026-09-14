package main

import "encoding/binary"

const (
	kindData        = 0
	kindGraph       = 1
	kindRegistry    = 2
	headerTransport = 8 + 1
	maxPacket       = 65535
	maxHops         = 16
)

func headerWord(kind byte, id uint64) uint64 {
	return id<<2 | uint64(kind)
}

func packetKind(packet []byte) byte {
	return packet[0] & 3
}

func packetID(packet []byte) uint64 {
	return binary.LittleEndian.Uint64(packet) >> 2
}

func packetSender(packet []byte) uint16 {
	return uint16(packet[8])
}

func validPacketType(kind byte) bool {
	return kind < 3
}

type Data struct {
	hops    []uint16
	cursor  int
	payload []byte
}

func encodeData(d *Data) []byte {
	out := make([]byte, 0, 1+len(d.hops)+len(d.payload))

	out = append(out, byte(len(d.hops)-1)<<4|byte(d.cursor))

	for _, hop := range d.hops {
		out = append(out, byte(hop))
	}

	return append(out, d.payload...)
}

func decodeData(inner []byte) (*Data, bool) {
	if len(inner) < 1 {
		return nil, false
	}

	count := int(inner[0]>>4) + 1
	cursor := int(inner[0] & 15)
	head := 1 + count

	if len(inner) < head || cursor >= count {
		return nil, false
	}

	d := &Data{hops: make([]uint16, count), cursor: cursor, payload: inner[head:]}

	for i := range d.hops {
		if inner[1+i] == 0 {
			return nil, false
		}

		d.hops[i] = uint16(inner[1+i])
	}

	return d, true
}

func advanceCursor(inner []byte, cursor int) {
	inner[0] = inner[0]&0xf0 | byte(cursor)
}
