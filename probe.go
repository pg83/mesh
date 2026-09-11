//go:build meshprobe

package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"github.com/coder/websocket"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

type ProbeCommand struct {
	Op   string          `json:"op"`
	Hex  string          `json:"hex"`
	Body json.RawMessage `json:"body"`
	Read bool            `json:"read"`
	Text bool            `json:"text"`
}

func main() {
	if os.Args[1] == "proxy" {
		target := throw2(url.Parse(os.Args[3]))

		throw(http.ListenAndServeTLS(os.Args[2], os.Args[4], os.Args[5], httputil.NewSingleHostReverseProxy(target)))

		return
	}

	cfg := loadConfig(os.Args[1])
	reg := newRegistry(cfg.Registry)
	peer := reg.byIndex[uint16(throw2(json.Number(os.Args[3]).Int64()))]
	dh := deriveKey(decodeKey(cfg.Key))
	session := newSession(reg.byIndex[cfg.Index], peer, dh.private)
	packetID := uint64(time.Now().UnixNano())

	var sendPacket func([]byte, bool)
	var ws *websocket.Conn

	if strings.HasPrefix(os.Args[2], "ws") {
		ws, _ = throw3(websocket.Dial(context.Background(), os.Args[2], nil))

		defer ws.CloseNow()

		sendPacket = func(packet []byte, text bool) {
			kind := websocket.MessageBinary

			if text {
				kind = websocket.MessageText
			}

			throw(ws.Write(context.Background(), kind, packet))
		}
	} else {
		remote := parseUDPAddr(os.Args[2])
		conn := throw2(net.ListenUDP("udp4", cfg.Endpoint[0].binding()))

		defer conn.Close()

		sendPacket = func(packet []byte, text bool) { throw2(conn.WriteToUDP(packet, remote)) }
	}

	encoder := json.NewEncoder(os.Stdout)

	throw(encoder.Encode(map[string]any{"ready": true}))

	scanner := bufio.NewScanner(os.Stdin)

	for scanner.Scan() {
		var command ProbeCommand

		throw(json.Unmarshal(scanner.Bytes(), &command))

		packetID++

		var out []byte

		switch command.Op {
		case "raw":
			out = throw2(hex.DecodeString(command.Hex))
		case "binding":
			out = session.seal(append([]byte{innerBinding}, command.Body...), packetID)
		case "inner":
			out = session.seal(throw2(hex.DecodeString(command.Hex)), packetID)
		case "short-transport":
			out = binary.LittleEndian.AppendUint16([]byte{packetTransport}, cfg.Index)
			out = binary.LittleEndian.AppendUint64(out, packetID)
		case "short-tag":
			out = session.seal(nil, packetID)
			out = out[:len(out)-1]
		case "ad":
			ad := &Ad{}

			throw(json.Unmarshal(command.Body, ad))

			out = session.seal(encodeAd(ad), packetID)
		default:
			throwFmt("unknown probe command %q", command.Op)
		}

		sendPacket(out, command.Text)

		report := map[string]any{"sent": true}

		if command.Read {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, packet, err := ws.Read(ctx)

			report["closed"] = err != nil && ctx.Err() == nil
			cancel()
			report["hex"] = hex.EncodeToString(packet)
		}

		throw(encoder.Encode(report))
	}

	throw(scanner.Err())
}
