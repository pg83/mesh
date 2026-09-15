package main

import "github.com/klauspost/compress/zstd"

var (
	compressor, _   = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
	decompressor, _ = zstd.NewReader(nil, zstd.WithDecoderMaxMemory(maxInflated), zstd.WithDecoderConcurrency(1))
)

const maxInflated = 64 << 10

func compress(raw []byte) []byte {
	return compressor.EncodeAll(raw, make([]byte, 0, len(raw)/2+32))
}

func decompress(packed []byte) ([]byte, bool) {
	raw, err := decompressor.DecodeAll(packed, nil)

	return raw, err == nil
}
