package main

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
)

func validPacketType(kind byte) bool {
	return kind == packetTransport || kind == packetGraph || kind == packetRegistry
}

type Data struct {
	hops    []uint16
	cursor  int
	payload []byte
}

func encodeData(d *Data) []byte {
	out := make([]byte, 0, 3+len(d.hops)+len(d.payload))

	out = append(out, innerData, byte(len(d.hops)), byte(d.cursor))

	for _, hop := range d.hops {
		out = append(out, byte(hop))
	}

	return append(out, d.payload...)
}

func decodeData(inner []byte) (*Data, bool) {
	if len(inner) < 3 {
		return nil, false
	}

	count := int(inner[1])
	head := 3 + count

	if count == 0 || count > maxHops || len(inner) < head || int(inner[2]) >= count {
		return nil, false
	}

	d := &Data{hops: make([]uint16, count), cursor: int(inner[2]), payload: inner[head:]}

	for i := range d.hops {
		if inner[3+i] == 0 {
			return nil, false
		}

		d.hops[i] = uint16(inner[3+i])
	}

	return d, true
}

func advanceCursor(inner []byte, cursor int) {
	inner[2] = byte(cursor)
}
