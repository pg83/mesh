package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

type WSListener struct {
	server  *http.Server
	conn    net.Listener
	proto   string
	tls     *tls.Config
	started bool
}

func (n *Node) listenWS(config ListenerBinding) string {
	c := config.config
	proto := c.BindProto

	if proto == "" {
		proto = c.Proto
	}

	if proto != "ws" && proto != "wss" {
		throwFmt("bad bind_proto %q", proto)
	}

	bind := config.bind
	key := bind.socketKey()
	host := bind.Addr.String()
	address := net.JoinHostPort(host, strconv.Itoa(int(key.port)))
	shared := address
	wildcard := net.JoinHostPort(key.wildcard(), strconv.Itoa(int(key.port)))

	if n.listeners[wildcard] != nil {
		shared = wildcard
	}

	if existing := n.listeners[shared]; existing != nil {
		if existing.proto != proto {
			throwFmt("conflicting listener protocols")
		}

		return shared
	}

	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	listener := net.Listener(throw2(net.Listen(key.network("tcp"), address)))

	if proto == "wss" {
		loaded := map[string]bool{}

		for _, entry := range n.endpoints {
			other := entry.config

			if other.TLSCert == "" || entry.bind.Port != bind.Port || entry.bind.ipv6() != bind.ipv6() || (!bind.ip().IsUnspecified() && entry.bind != bind) || loaded[other.TLSCert] {
				continue
			}

			tlsConfig.Certificates = append(tlsConfig.Certificates, throw2(tls.LoadX509KeyPair(other.TLSCert, other.TLSKey)))
			loaded[other.TLSCert] = true
		}

		if len(tlsConfig.Certificates) == 0 {
			throwFmt("TLS listener needs tls_cert and tls_key")
		}

		listener = tls.NewListener(listener, tlsConfig)
	}

	server := &http.Server{Handler: http.HandlerFunc(n.acceptWS), ReadHeaderTimeout: time.Minute}

	n.listeners[address] = &WSListener{server: server, conn: listener, proto: proto, tls: tlsConfig}

	return address
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

		var target Vertex

		for id, local := range view.local {
			ep := view.addresses[id]

			if ep.isEndpoint() && ep.Proto != "udp" && local.address == socketAddress(address.IP, address.Port) && ep.Path == r.URL.RequestURI() && strings.EqualFold(ep.Addr, host) {
				if target.hash() != 0 {
					throwFmt("ambiguous websocket listener")
				}

				target = ep
			}
		}

		if target.hash() == 0 {
			http.NotFound(w, r)

			return
		}

		socket := throw2(websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{protocol}}))

		defer socket.CloseNow()

		socket.SetReadLimit(maxPacket)

		packet := readWS(ctx, socket)
		session, source, _, ok := n.readPacket(packet, view)

		if !ok || source.Proto != "tcp" {
			return
		}

		c := newWSConnection(socket, session, target, source, source.hash(), binary.LittleEndian.Uint64(packet[3:]))

		c.send.local, c.receive.local = view.local[target.hash()], view.local[target.hash()]

		read := c.receive.read

		c.receive.read = func(input chan any) {
			select {
			case input <- Received{packet: packet, at: time.Now(), io: c.receive}:
			case <-c.receive.ctx.Done():
			}

			read(input)
		}

		post(n.events.in, any(c.send))
		post(n.events.in, any(c.receive))

		<-c.done
	}).catch(func(e *Exception) { n.log.Debug("websocket accept failed", "err", e) })
}

func readWS(ctx context.Context, conn *websocket.Conn) []byte {
	kind, packet := throw3(conn.Read(ctx))

	if kind != websocket.MessageBinary {
		throwFmt("non-binary websocket message")
	}

	return packet
}

func (n *Node) dialWebSocket(ctx context.Context, local *LocalAddress, target Endpoint) (*websocket.Conn, *net.TCPAddr) {
	var address *net.TCPAddr

	transport := &http.Transport{DialContext: func(ctx context.Context, network, remote string) (net.Conn, error) {
		conn, err := n.socketPool.tcp(ctx, local, remote)

		if err == nil {
			address = conn.LocalAddr().(*net.TCPAddr)
		}

		return conn, err
	}, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}

	defer transport.CloseIdleConnections()

	if ca := n.tlsCA[target.hash()]; ca != "" {
		pool := throw2(x509.SystemCertPool())

		if !pool.AppendCertsFromPEM(throw2(os.ReadFile(ca))) {
			throwFmt("invalid TLS CA")
		}

		transport.TLSClientConfig.RootCAs = pool
	}

	socket, _ := throw3(websocket.Dial(ctx, target.url(), &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}, Subprotocols: []string{protocol}}))

	socket.SetReadLimit(maxPacket)

	return socket, address
}
