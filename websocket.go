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
	"sync"
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
	peer   uint16
	origin uint64
	id     uint64
	queue  chan []byte
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
}

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
		address := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
		host := r.Host

		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}

		destinations := map[uint64]bool{}

		n.mu.Lock()

		for id, local := range n.local {
			ep := n.addresses[id]

			if local.socket == nil && local.address == endpoint(address.IP, address.Port) && ep.Path == r.URL.RequestURI() && strings.EqualFold(ep.Addr, host) {
				destinations[id] = true
			}
		}

		n.mu.Unlock()

		if len(destinations) == 0 {
			http.NotFound(w, r)

			return
		}

		conn := throw2(websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{protocol}}))

		defer conn.CloseNow()

		conn.SetReadLimit(maxPacket)

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)

		defer cancel()

		packet := readWS(ctx, conn)

		n.mu.Lock()

		session, binding, ok := n.readBinding(packet)

		if !ok || !destinations[binding.To.hash()] {
			n.mu.Unlock()

			return
		}

		edge := Edge{From: n.remember(binding.To), To: n.remember(binding.From)}
		response := n.bindingPacket(session, edge)

		n.mu.Unlock()
		throw(conn.Write(ctx, websocket.MessageBinary, response))
		n.serveWS(conn, edge, session.peer, edge.To, binary.LittleEndian.Uint64(packet[3:]))
	}).catch(func(e *Exception) { n.log.Debug("websocket accept failed", "err", e) })
}

func readWS(ctx context.Context, conn *websocket.Conn) []byte {
	kind, packet := throw3(conn.Read(ctx))

	if kind != websocket.MessageBinary {
		throwFmt("non-binary websocket message")
	}

	return packet
}

func (n *Node) readBinding(packet []byte) (*Session, WSBinding, bool) {
	binding := WSBinding{}

	if len(packet) < headerTransport {
		return nil, binding, false
	}

	session := n.peers[binary.LittleEndian.Uint16(packet[1:])]

	if session == nil {
		return nil, binding, false
	}

	inner, ok := session.open(packet)

	if !ok || len(inner) == 0 || inner[0] != innerBinding || json.Unmarshal(inner[1:], &binding) != nil {
		return nil, binding, false
	}

	binding.From = binding.From.canonical()
	binding.To = binding.To.canonical()

	if !binding.From.valid() || !binding.To.valid() || binding.From.Proto == "udp" || binding.To.Proto == "udp" {
		return nil, binding, false
	}

	if owner := n.owners[binding.From.hash()]; owner != 0 && owner != session.peer {
		return nil, binding, false
	}

	return session, binding, true
}

func (n *Node) bindingPacket(session *Session, edge Edge) []byte {
	blob := throw2(json.Marshal(WSBinding{From: n.addresses[edge.From], To: n.addresses[edge.To]}))

	return session.seal(append([]byte{innerBinding}, blob...), n.nextPacketID())
}

func (n *Node) sendWS(packet []byte, edge Edge, peer uint16) {
	dst := n.addresses[edge.To]

	if dst.Proto != "ws" && dst.Proto != "wss" {
		return
	}

	key := connectionKey(edge)

	if conn := n.ws[key]; conn != nil {
		select {
		case conn.queue <- packet:
		default:
		}

		return
	}

	if n.dialing[key] != 0 {
		return
	}

	source := n.local[edge.From].address.ip()
	ca := n.tlsCA[edge.To]
	first := n.bindingPacket(n.peers[peer], edge)

	n.dialing[key] = binary.LittleEndian.Uint64(first[3:])

	go n.dialWS(edge, peer, source, dst, ca, first)
}

func (n *Node) dialWS(edge Edge, peer uint16, source net.IP, dst Endpoint, ca string, first []byte) {
	defer func() {
		n.mu.Lock()

		if n.dialing[connectionKey(edge)] == binary.LittleEndian.Uint64(first[3:]) {
			delete(n.dialing, connectionKey(edge))
		}

		n.mu.Unlock()
	}()

	try(func() {
		dialer := &net.Dialer{LocalAddr: &net.TCPAddr{IP: source}}
		transport := &http.Transport{DialContext: dialer.DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}

		defer transport.CloseIdleConnections()

		if ca != "" {
			pool := throw2(x509.SystemCertPool())

			if !pool.AppendCertsFromPEM(throw2(os.ReadFile(ca))) {
				throwFmt("invalid TLS CA")
			}

			transport.TLSClientConfig.RootCAs = pool
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)

		defer cancel()

		conn, _ := throw3(websocket.Dial(ctx, dst.url(), &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}, Subprotocols: []string{protocol}}))

		defer conn.CloseNow()

		conn.SetReadLimit(maxPacket)
		throw(conn.Write(ctx, websocket.MessageBinary, first))

		packet := readWS(ctx, conn)

		n.mu.Lock()

		session, binding, ok := n.readBinding(packet)

		n.mu.Unlock()

		if !ok || session.peer != peer || binding.From.hash() != edge.To || binding.To.hash() != edge.From {
			return
		}

		n.serveWS(conn, edge, peer, edge.From, binary.LittleEndian.Uint64(first[3:]))
	}).catch(func(e *Exception) { n.log.Debug("websocket dial failed", "endpoint", dst.string(), "err", e) })
}

func (c *WSConnection) stop() {
	c.once.Do(func() { c.cancel(); c.conn.CloseNow() })
}

func (n *Node) serveWS(socket *websocket.Conn, edge Edge, peer uint16, origin, id uint64) {
	ctx, cancel := context.WithCancel(context.Background())
	conn := &WSConnection{conn: socket, edge: edge, peer: peer, origin: origin, id: id, queue: make(chan []byte, 64), ctx: ctx, cancel: cancel}

	defer conn.stop()

	key := connectionKey(edge)

	n.mu.Lock()

	if origin == edge.From && n.dialing[key] == id {
		delete(n.dialing, key)
	}

	old := n.ws[key]

	if n.local[edge.From] == nil || (old != nil && (old.origin < origin || (old.origin == origin && old.id >= id))) {
		n.mu.Unlock()

		return
	}

	n.ws[key] = conn
	n.mu.Unlock()

	if old != nil {
		old.stop()
	}

	defer func() {
		n.mu.Lock()

		if n.ws[key] == conn {
			delete(n.ws, key)
		}

		n.mu.Unlock()
	}()

	go func() {
		defer conn.stop()

		try(func() {
			for {
				select {
				case packet := <-conn.queue:
					throw(socket.Write(ctx, websocket.MessageBinary, packet))
				case <-ctx.Done():
					return
				}
			}
		}).catch(func(e *Exception) { n.log.Debug("websocket writer stopped", "err", e) })
	}()

	for {
		packet := readWS(ctx, socket)

		n.mu.Lock()

		if n.ws[key] == conn && n.local[edge.From] != nil && len(packet) >= headerTransport && binary.LittleEndian.Uint16(packet[1:]) == peer && (packet[0] == packetTransport || packet[0] == packetGossip) {
			n.handleTransport(packet, Edge{From: edge.To, To: edge.From})
		}

		n.mu.Unlock()
	}
}
