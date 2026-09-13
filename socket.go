package main

import (
	"context"
	"net"
	"net/url"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/net/ipv4"
)

type SocketEndpoint struct {
	public Endpoint
	bind   Endpoint
}

func (c EndpointConfig) validate() {
	if c.Proto != "udp" && c.Proto != "ws" && c.Proto != "wss" {
		throwFmt("bad endpoint proto %q", c.Proto)
	}

	if c.Port < 1 || c.Port > 65535 || c.BindPort < 0 || c.BindPort > 65535 {
		throwFmt("bad endpoint port")
	}

	if c.Proto != "udp" {
		path := c.description().Path

		if !strings.HasPrefix(path, "/") {
			throwFmt("bad websocket path")
		}

		if _, err := url.ParseRequestURI(path); err != nil {
			throwFmt("bad websocket path: %s", err)
		}
	}
}

func (c EndpointConfig) address() *net.UDPAddr {
	return parseUDPAddr(net.JoinHostPort(c.Addr, strconv.Itoa(c.Port)))
}

func (c EndpointConfig) binding() *net.UDPAddr {
	addr, port := c.BindAddr, c.BindPort

	if addr == "" {
		addr = c.Addr
	}

	if port == 0 {
		port = c.Port
	}

	return parseUDPAddr(net.JoinHostPort(addr, strconv.Itoa(port)))
}

func (c EndpointConfig) description() Endpoint {
	if c.Proto == "udp" {
		a := c.address()

		return endpoint(a.IP, a.Port)
	}

	return (Endpoint{Proto: c.Proto, Addr: c.Addr, Port: uint16(c.Port), Path: c.Path}).canonical()
}

func reuseUDP(network, address string, raw syscall.RawConn) error {
	var result error

	err := raw.Control(func(fd uintptr) { result = socketReuse(int(fd)) })

	if err != nil {
		result = err
	}

	return result
}

func newUDPSocket(port uint16) *UDPSocket {
	guard := udpGuard(port)
	lc := net.ListenConfig{Control: reuseUDP}
	udp := throw2(lc.ListenPacket(context.Background(), "udp4", net.JoinHostPort("0.0.0.0", strconv.Itoa(int(port))))).(*net.UDPConn)

	throw(udp.SetReadBuffer(1 << 20))

	conn := ipv4.NewPacketConn(udp)

	throw(conn.SetControlMessage(ipv4.FlagDst, true))

	return &UDPSocket{conn: conn, port: port, guard: guard}
}

func connectUDP(local *LocalEndpoint, remote Endpoint) *net.UDPConn {
	dialer := net.Dialer{LocalAddr: local.address.addr(), Control: reuseUDP}
	conn := throw2(dialer.Dial("udp4", remote.string())).(*net.UDPConn)

	throw(conn.SetReadBuffer(1 << 20))

	return conn
}
