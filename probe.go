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
	Op     string          `json:"op"`
	Hex    string          `json:"hex"`
	Body   json.RawMessage `json:"body"`
	Read   bool            `json:"read"`
	Text   bool            `json:"text"`
	ID     uint64          `json:"id"`
	Source *Vertex         `json:"source,omitempty"`
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
	var source, target Vertex
	var ws *websocket.Conn
	var halves *WSConnection
	var received *Mailbox[any]

	if strings.HasPrefix(os.Args[2], "ws") {
		transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, address)

			if err == nil {
				source = socketVertex(conn.LocalAddr())
			}

			return conn, err
		}}

		ws, _ = throw3(websocket.Dial(context.Background(), os.Args[2], &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}}))

		uri := throw2(url.Parse(os.Args[2]))
		port := throw2(json.Number(uri.Port()).Int64())

		target = Vertex{Proto: uri.Scheme, Addr: uri.Hostname(), Port: uint16(port), Path: uri.RequestURI(), Endpoint: true}

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

		source = socketVertex(conn.LocalAddr())
		target = udpVertex(remote.IP, remote.Port)
		sendPacket = func(packet []byte, text bool) { throw2(conn.Write(packet)) }
	}

	encoder := json.NewEncoder(os.Stdout)

	throw(encoder.Encode(map[string]any{"ready": true, "source": source}))

	scanner := bufio.NewScanner(os.Stdin)

	for scanner.Scan() {
		var command ProbeCommand

		throw(json.Unmarshal(scanner.Bytes(), &command))

		packetID++

		if command.ID != 0 {
			packetID = command.ID
		}

		var out []byte

		from := source

		if command.Source != nil {
			from = *command.Source
		}

		switch command.Op {
		case "wrap":
			halves = newWSConnection(ws, session, source, target, source.hash(), packetID)
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
		case "inner":
			out = session.seal(from, throw2(hex.DecodeString(command.Hex)), packetID)
		case "short-transport":
			out = binary.LittleEndian.AppendUint16([]byte{packetTransport}, cfg.Index)
			out = binary.LittleEndian.AppendUint64(out, packetID)
		case "short-tag":
			out = session.seal(from, nil, packetID)
			out = out[:len(out)-1]
		case "edges":
			var edges EdgeRecords

			throw(json.Unmarshal(command.Body, &edges))

			out = session.seal(from, encodeEdges(edges), packetID)
		case "vertices":
			var vertices VertexRecords

			throw(json.Unmarshal(command.Body, &vertices))

			out = session.seal(from, encodeVertices(vertices), packetID)
		case "open":
			origin, inner, ok := session.open(throw2(hex.DecodeString(command.Hex)))

			throw(encoder.Encode(map[string]any{"opened": ok, "hex": hex.EncodeToString(inner), "source": origin}))

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
