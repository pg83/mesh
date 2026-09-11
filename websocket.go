package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

type WSListener struct {
	server *http.Server
	conn   net.Listener
	proto  string
	tls    *tls.Config
}
type WSBinding struct {
	From Endpoint `json:"from"`
	To   Endpoint `json:"to"`
}
type WSConnection struct {
	conn   *websocket.Conn
	edge   Edge
	source Endpoint
	target Endpoint
	peer   uint16
	origin uint64
	id     uint64
	queue  *Mailbox[[]byte]
	ctx    context.Context
	cancel context.CancelFunc
}

type DialResult struct{ conn *WSConnection }

func connectionKey(edge Edge) Edge {
	if edge.From > edge.To {
		return Edge{From: edge.To, To: edge.From}
	}

	return edge
}

func (n *Node) listenWS(c EndpointConfig) {
	proto := c.BindProto

	if proto == "" {
		proto = c.Proto
	}

	if proto != "ws" && proto != "wss" {
		throwFmt("bad bind_proto %q", proto)
	}

	address := net.JoinHostPort("0.0.0.0", strconv.Itoa(c.binding().Port))

	if existing := n.listeners[address]; existing != nil {
		if existing.proto != proto {
			throwFmt("conflicting listener protocols")
		}

		if proto == "wss" && c.TLSCert != "" {
			existing.tls.Certificates = append(existing.tls.Certificates, throw2(tls.LoadX509KeyPair(c.TLSCert, c.TLSKey)))
		}

		return
	}

	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	listener := net.Listener(throw2(net.Listen("tcp4", address)))

	if proto == "wss" {
		pair := throw2(tls.LoadX509KeyPair(c.TLSCert, c.TLSKey))

		tlsConfig.Certificates = []tls.Certificate{pair}
		listener = tls.NewListener(listener, tlsConfig)
	}

	server := &http.Server{Handler: http.HandlerFunc(n.acceptWS), ReadHeaderTimeout: time.Minute}

	n.listeners[address] = &WSListener{server: server, conn: listener, proto: proto, tls: tlsConfig}
}

func (n *Node) acceptWS(w http.ResponseWriter, r *http.Request) {
	try(func() {
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)

		defer cancel()

		view := n.currentSnapshot(ctx)
		address := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
		host := r.Host

		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}

		destinations := map[uint64]bool{}

		for id, local := range view.local {
			ep := view.addresses[id]

			if local.socket == nil && local.address == endpoint(address.IP, address.Port) && ep.Path == r.URL.RequestURI() && strings.EqualFold(ep.Addr, host) {
				destinations[id] = true
			}
		}

		if len(destinations) == 0 {
			http.NotFound(w, r)

			return
		}

		socket := throw2(websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{protocol}}))

		defer socket.CloseNow()

		socket.SetReadLimit(maxPacket)

		packet := readWS(ctx, socket)
		session, binding, ok := n.readBinding(packet, view)

		if !ok || !destinations[binding.To.hash()] {
			return
		}

		response := bindingPacket(session, binding.To, binding.From, uint64(time.Now().UnixNano()))

		throw(socket.Write(ctx, websocket.MessageBinary, response))

		c := newWSConnection(socket, binding.To, binding.From, session.peer, binding.From.hash(), binary.LittleEndian.Uint64(packet[3:]))

		n.events.in <- c

		<-c.ctx.Done()
	}).catch(func(e *Exception) { n.log.Debug("websocket accept failed", "err", e) })
}

func readWS(ctx context.Context, conn *websocket.Conn) []byte {
	kind, packet := throw3(conn.Read(ctx))

	if kind != websocket.MessageBinary {
		throwFmt("non-binary websocket message")
	}

	return packet
}

func (n *Node) readBinding(packet []byte, view *Snapshot) (*Session, WSBinding, bool) {
	binding := WSBinding{}

	if len(packet) < headerTransport {
		return nil, binding, false
	}

	peer := binary.LittleEndian.Uint16(packet[1:])

	if peer == n.cfg.Index || n.reg.byIndex[peer] == nil {
		return nil, binding, false
	}

	session := n.session(peer)
	inner, ok := session.open(packet)

	if !ok || len(inner) == 0 || inner[0] != innerBinding || json.Unmarshal(inner[1:], &binding) != nil {
		return nil, binding, false
	}

	binding.From = binding.From.canonical()
	binding.To = binding.To.canonical()

	if !binding.From.valid() || !binding.To.valid() || binding.From.Proto == "udp" || binding.To.Proto == "udp" {
		return nil, binding, false
	}

	if owner := view.owners[binding.From.hash()]; owner != 0 && owner != peer {
		return nil, binding, false
	}

	return session, binding, true
}

func bindingPacket(session *Session, source, target Endpoint, id uint64) []byte {
	blob := throw2(json.Marshal(WSBinding{From: source, To: target}))

	return session.seal(append([]byte{innerBinding}, blob...), id)
}

func newWSConnection(socket *websocket.Conn, source, target Endpoint, peer uint16, origin, id uint64) *WSConnection {
	ctx, cancel := context.WithCancel(context.Background())

	return &WSConnection{conn: socket, edge: Edge{From: source.hash(), To: target.hash()}, source: source, target: target,
		peer: peer, origin: origin, id: id, queue: newMailbox[[]byte](ctx.Done()), ctx: ctx, cancel: cancel}
}

func (c *WSConnection) stop() {
	c.cancel()
}

func (a *EdgeActor) sendWS(packet []byte) {
	if a.ws != nil {
		select {
		case a.ws.queue.in <- packet:
		case <-a.ws.ctx.Done():
		}

		return
	}

	if a.dial != nil {
		return
	}

	local := a.view.local[a.edge.From]

	if local == nil {
		return
	}

	result := make(chan DialResult, 1)

	a.dial = result
	a.packetID++

	source, target := a.view.addresses[a.edge.From], a.view.addresses[a.edge.To]
	first := bindingPacket(a.session, source, target, a.packetID)
	view := a.view

	go a.node.dialWS(result, view, local.address.ip(), source, target, a.peer, first)
	a.report()
}

func (n *Node) dialWS(result chan<- DialResult, view *Snapshot, local net.IP, source, target Endpoint, peer uint16, first []byte) {
	var socket *websocket.Conn
	var established *WSConnection

	defer func() {
		if established == nil && socket != nil {
			socket.CloseNow()
		}

		result <- DialResult{conn: established}
	}()

	try(func() {
		dialer := &net.Dialer{LocalAddr: &net.TCPAddr{IP: local}}
		transport := &http.Transport{DialContext: dialer.DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}

		defer transport.CloseIdleConnections()

		if ca := n.tlsCA[target.hash()]; ca != "" {
			pool := throw2(x509.SystemCertPool())

			if !pool.AppendCertsFromPEM(throw2(os.ReadFile(ca))) {
				throwFmt("invalid TLS CA")
			}

			transport.TLSClientConfig.RootCAs = pool
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)

		defer cancel()

		socket, _ = throw3(websocket.Dial(ctx, target.url(), &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}, Subprotocols: []string{protocol}}))
		socket.SetReadLimit(maxPacket)
		throw(socket.Write(ctx, websocket.MessageBinary, first))

		packet := readWS(ctx, socket)
		session, binding, ok := n.readBinding(packet, view)

		if !ok || session.peer != peer || binding.From.hash() != target.hash() || binding.To.hash() != source.hash() {
			return
		}

		established = newWSConnection(socket, source, target, peer, source.hash(), binary.LittleEndian.Uint64(first[3:]))
	}).catch(func(e *Exception) { n.log.Debug("websocket dial failed", "endpoint", target.string(), "err", e) })
}

func (a *EdgeActor) attachWS(c *WSConnection) {
	old := a.ws

	if a.view == nil || a.view.local[a.edge.From] == nil || (old != nil && (old.origin < c.origin || (old.origin == c.origin && old.id >= c.id))) {
		c.stop()
		c.conn.CloseNow()

		return
	}

	if old != nil {
		old.stop()
	}

	a.ws = c

	input := a.view.actors[Edge{From: a.edge.To, To: a.edge.From}]

	go c.run(input, a.node)
	a.report()
}

func (c *WSConnection) run(input chan any, n *Node) {
	defer c.conn.CloseNow()

	defer c.stop()

	errors := make(chan *Exception, 2)

	go func() {
		errors <- try(func() {
			for {
				packet := readWS(c.ctx, c.conn)

				post(input, any(Received{packet: packet, at: time.Now(), ws: c}))
			}
		})
	}()

	go func() {
		errors <- try(func() {
			for {
				select {
				case packet := <-c.queue.out:
					throw(c.conn.Write(c.ctx, websocket.MessageBinary, packet))
				case <-c.ctx.Done():
					return
				}
			}
		})
	}()

	select {
	case err := <-errors:
		err.catch(func(e *Exception) { n.log.Debug("websocket stopped", "err", e) })
	case <-c.ctx.Done():
	}
}
