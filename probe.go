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
	ID   uint64          `json:"id"`
}

func main() {
	if os.Args[1] == "echo" {
		echoProbe(os.Args[2], os.Args[3], uint16(throw2(json.Number(os.Args[4]).Int64())))

		return
	}

	if os.Args[1] == "proxy" {
		target := throw2(url.Parse(os.Args[3]))

		throw(http.ListenAndServeTLS(os.Args[2], os.Args[4], os.Args[5], httputil.NewSingleHostReverseProxy(target)))

		return
	}

	cfg := loadConfig(os.Args[1])
	reg := newRegistry(cfg.Registry, cfg.RegistryVersion)
	peer := reg.byIndex[uint16(throw2(json.Number(os.Args[3]).Int64()))]
	dh := deriveKey(decodeKey(cfg.Key))
	session := newSession(reg.byIndex[cfg.Index], peer, dh.private)
	packetID := uint64(time.Now().UnixNano())

	var sendPacket func([]byte, bool)
	var ws *websocket.Conn
	var halves *WSConnection
	var received *Mailbox[any]

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
		conn := throw2(net.DialUDP(cfg.Endpoint[0].description().socketKey().network("udp"), &net.UDPAddr{IP: cfg.Endpoint[0].binding().IP}, remote))

		defer conn.Close()

		source := sourceVertex(cfg.Index, conn.LocalAddr().(*net.UDPAddr).IP)
		first := bindingPacket(session, source, udpVertex(remote.IP, remote.Port), packetID)

		throw2(conn.Write(first))

		sendPacket = func(packet []byte, text bool) { throw2(conn.Write(packet)) }
	}

	encoder := json.NewEncoder(os.Stdout)

	throw(encoder.Encode(map[string]any{"ready": true}))

	scanner := bufio.NewScanner(os.Stdin)

	for scanner.Scan() {
		var command ProbeCommand

		throw(json.Unmarshal(scanner.Bytes(), &command))

		packetID++

		if command.ID != 0 {
			packetID = command.ID
		}

		var out []byte

		switch command.Op {
		case "wrap":
			var binding Binding
			throw(json.Unmarshal(command.Body, &binding))
			halves = newWSConnection(ws, session, binding.From, binding.To, binding.From.hash(), packetID)
			received = newMailbox[any](halves.done)
			go halves.receive.read(received.in)
			sendPacket = func(packet []byte, text bool) {
				throw(halves.send.ctx.Err())
				halves.send.write(context.Background(), packet)
			}
			throw(encoder.Encode(map[string]bool{"wrapped": true}))

			continue
		case "stop-send", "stop-receive":
			if command.Op == "stop-send" {
				halves.send.stop()
			} else {
				halves.receive.stop()
			}

			closed := halves.send.ctx.Err() != nil && halves.receive.ctx.Err() != nil

			if closed {
				<-halves.done
			}

			throw(encoder.Encode(map[string]bool{"closed": closed}))

			continue
		case "read":
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
		case "edges":
			var edges EdgeRecords

			throw(json.Unmarshal(command.Body, &edges))

			out = session.seal(encodeEdges(edges), packetID)
		case "vertices":
			var vertices VertexRecords

			throw(json.Unmarshal(command.Body, &vertices))

			out = session.seal(encodeVertices(vertices), packetID)
		case "open":
			inner, ok := session.open(throw2(hex.DecodeString(command.Hex)))

			throw(encoder.Encode(map[string]any{"opened": ok, "hex": hex.EncodeToString(inner)}))

			continue
		default:
			throwFmt("unknown probe command %q", command.Op)
		}

		if command.Op != "read" {
			sendPacket(out, command.Text)
		}

		report := map[string]any{"sent": true}

		if command.Read {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

			var packet []byte

			if halves == nil {
				_, p, err := ws.Read(ctx)

				packet = p
				report["closed"] = err != nil && ctx.Err() == nil
			} else {
				select {
				case message := <-received.out:
					packet = message.(Received).packet
					report["closed"] = false
				case <-halves.done:
					report["closed"] = true
				case <-ctx.Done():
					throw(ctx.Err())
				}
			}

			cancel()
			report["hex"] = hex.EncodeToString(packet)
		}

		throw(encoder.Encode(report))
	}

	throw(scanner.Err())
}
