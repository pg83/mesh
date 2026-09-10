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

	"github.com/flynn/noise"
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

	hs := throw2(noise.NewHandshakeState(noise.Config{
		CipherSuite: cipherSuite, Pattern: noise.HandshakeIK, Initiator: true,
		Prologue: []byte(prologue), StaticKeypair: dh, PeerStatic: peer.pub,
	}))

	conn := throw2(net.ListenUDP("udp4", &net.UDPAddr{Port: cfg.Port}))

	defer conn.Close()

	msg, _, _, err := hs.WriteMessage(nil, binary.BigEndian.AppendUint64(nil, uint64(time.Now().UnixNano())))

	throw(err)

	packet := binary.BigEndian.AppendUint32([]byte{packetInit}, 42)

	throw2(conn.WriteToUDP(append(packet, msg...), remote))
	throw(conn.SetReadDeadline(time.Now().Add(5 * time.Second)))

	var session *Session

	buf := make([]byte, maxPacket)

	for session == nil {
		size, _, err := conn.ReadFromUDP(buf)

		throw(err)

		if size < headerResponse || buf[0] != packetResponse {
			continue
		}

		_, send, recv, err := hs.ReadMessage(nil, buf[headerResponse:size])

		throw(err)

		session = newSession(peer.index, 42, binary.BigEndian.Uint32(buf[5:]), send, recv, remote, time.Now())
	}

	encoder := json.NewEncoder(os.Stdout)

	throw(encoder.Encode(map[string]any{"ready": true}))

	scanner := bufio.NewScanner(os.Stdin)

	for scanner.Scan() {
		var command ProbeCommand

		throw(json.Unmarshal(scanner.Bytes(), &command))

		var out []byte

		switch command.Op {
		case "inner":
			out = session.seal(throw2(hex.DecodeString(command.Hex)), time.Now())
		case "short-transport":
			out = binary.BigEndian.AppendUint32([]byte{packetTransport}, session.remoteID)
			out = binary.BigEndian.AppendUint64(out, 0)
		case "ad":
			key := sig

			if command.Key != "" {
				_, key = deriveKeys(decodeKey(command.Key))
			}

			inner := encodeAd(command.Body, ed25519.Sign(key, command.Body))

			out = session.seal(inner, time.Now())
		default:
			throwFmt("unknown probe command %q", command.Op)
		}

		throw2(conn.WriteToUDP(out, remote))
		throw(encoder.Encode(map[string]any{"sent": true}))
	}

	throw(scanner.Err())
}
