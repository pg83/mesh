//go:build meshprobe

package main

import (
	"bufio"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"time"
)

type ProbeCommand struct {
	Op   string          `json:"op"`
	Hex  string          `json:"hex"`
	Body json.RawMessage `json:"body"`
	Key  string          `json:"key"`
}

func main() {
	cfg := loadConfig(os.Args[1])
	remote := parseUDPAddr(os.Args[2])
	reg := newRegistry(cfg.Registry)
	peer := reg.byIndex[uint16(throw2(json.Number(os.Args[3]).Int64()))]
	dh, sig := deriveKeys(decodeKey(cfg.Key))
	session := newSession(reg.byIndex[cfg.Index], peer, dh.private)
	packetID := uint64(time.Now().UnixNano())
	conn := throw2(net.ListenUDP("udp4", &net.UDPAddr{Port: cfg.Port}))

	defer conn.Close()

	encoder := json.NewEncoder(os.Stdout)

	throw(encoder.Encode(map[string]any{"ready": true}))

	scanner := bufio.NewScanner(os.Stdin)

	for scanner.Scan() {
		var command ProbeCommand

		throw(json.Unmarshal(scanner.Bytes(), &command))

		packetID++

		var out []byte

		switch command.Op {
		case "inner":
			out = session.seal(throw2(hex.DecodeString(command.Hex)), packetID)
		case "short-transport":
			out = binary.LittleEndian.AppendUint16([]byte{packetTransport}, cfg.Index)
			out = binary.LittleEndian.AppendUint64(out, packetID)
		case "ad":
			key := sig

			if command.Key != "" {
				_, key = deriveKeys(decodeKey(command.Key))
			}

			inner := encodeAd(command.Body, ed25519.Sign(key, command.Body))

			out = session.seal(inner, packetID)
		default:
			throwFmt("unknown probe command %q", command.Op)
		}

		throw2(conn.WriteToUDP(out, remote))
		throw(encoder.Encode(map[string]any{"sent": true}))
	}

	throw(scanner.Err())
}
