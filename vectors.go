package main

import (
	"encoding/binary"
)

const maxInner = 1500 - 28 - headerTransport - sourceSize - 16

type Vector struct {
	Owner   uint16            `json:"owner"`
	Version uint64            `json:"version"`
	Records map[uint16]uint64 `json:"records"`
}

type vectorItem struct {
	vector  *Vector
	owner   uint16
	version uint64
}

func packBundles(items []vectorItem) [][]byte {
	packed := compress(encodeBundle(items))

	if len(packed) <= maxInner || len(items) <= 1 {
		return [][]byte{packed}
	}

	half := len(items) / 2

	return append(packBundles(items[:half]), packBundles(items[half:])...)
}

func encodeBundle(items []vectorItem) []byte {
	chunks := [][]vectorItem{}

	for i, item := range items {
		if i == 0 || item.vector != items[i-1].vector {
			chunks = append(chunks, nil)
		}

		chunks[len(chunks)-1] = append(chunks[len(chunks)-1], item)
	}

	out := []byte{byte(len(chunks))}

	for _, chunk := range chunks {
		out = append(out, byte(chunk[0].vector.Owner))
		out = binary.LittleEndian.AppendUint64(out, chunk[0].vector.Version)
		out = append(out, byte(len(chunk)))

		for _, item := range chunk {
			out = append(out, byte(item.owner))
			out = binary.LittleEndian.AppendUint64(out, item.version)
		}
	}

	return out
}

func decodeBundle(raw []byte) ([]*Vector, bool) {
	if len(raw) < 1 {
		return nil, false
	}

	chunks := make([]*Vector, 0, raw[0])

	raw = raw[1:]

	for range cap(chunks) {
		if len(raw) < 10 || raw[0] == 0 {
			return nil, false
		}

		chunk := &Vector{Owner: uint16(raw[0]), Version: binary.LittleEndian.Uint64(raw[1:]), Records: map[uint16]uint64{}}
		count := int(raw[9])

		raw = raw[10:]

		if len(raw) < count*9 {
			return nil, false
		}

		for range count {
			if raw[0] == 0 {
				return nil, false
			}

			chunk.Records[uint16(raw[0])] = binary.LittleEndian.Uint64(raw[1:])
			raw = raw[9:]
		}

		chunks = append(chunks, chunk)
	}

	if len(raw) != 0 {
		return nil, false
	}

	return chunks, true
}
