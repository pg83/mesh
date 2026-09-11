package main

import (
	"net"
	"net/url"
	"strconv"
	"strings"

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

func newUDPSocket(port uint16) *UDPSocket {
	conn := ipv4.NewPacketConn(throw2(net.ListenUDP("udp4", &net.UDPAddr{Port: int(port)})))

	throw(conn.SetControlMessage(ipv4.FlagDst, true))

	return &UDPSocket{conn: conn, port: port}
}
