package main

import (
	"context"
	"net"
	"net/url"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
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

func udpControl(iface int) func(string, string, syscall.RawConn) error {
	return func(network, address string, raw syscall.RawConn) error {
		var result error

		err := raw.Control(func(fd uintptr) { result = socketReuse(int(fd), iface, strings.HasSuffix(network, "6")) })

		if err != nil {
			result = err
		}

		return result
	}
}

func (k SocketKey) network(proto string) string {
	if k.ipv6 {
		return proto + "6"
	}

	return proto + "4"
}

func (k SocketKey) wildcard() string {
	if k.ipv6 {
		return "::"
	}

	return "0.0.0.0"
}

func newUDPSocket(key SocketKey) *UDPSocket {
	guard := udpGuard(key)
	lc := net.ListenConfig{Control: udpControl(0)}
	udp := throw2(lc.ListenPacket(context.Background(), key.network("udp"), net.JoinHostPort(key.wildcard(), strconv.Itoa(int(key.port))))).(*net.UDPConn)

	throw(udp.SetReadBuffer(1 << 20))

	socket := &UDPSocket{port: key.port, guard: guard}

	if key.ipv6 {
		conn := ipv6.NewPacketConn(udp)

		throw(conn.SetControlMessage(ipv6.FlagDst, true))
		socket.read = func(buf []byte) (int, net.IP, net.Addr, error) {
			size, control, remote, err := conn.ReadFrom(buf)

			var dst net.IP

			if control != nil {
				dst = control.Dst
			}

			return size, dst, remote, err
		}
	} else {
		conn := ipv4.NewPacketConn(udp)

		throw(conn.SetControlMessage(ipv4.FlagDst, true))
		socket.read = func(buf []byte) (int, net.IP, net.Addr, error) {
			size, control, remote, err := conn.ReadFrom(buf)

			var dst net.IP

			if control != nil {
				dst = control.Dst
			}

			return size, dst, remote, err
		}
	}

	return socket
}

func connectUDP(local *LocalEndpoint, remote Endpoint) *net.UDPConn {
	dialer := net.Dialer{LocalAddr: local.address.addr(), Control: udpControl(local.iface)}
	conn := throw2(dialer.Dial(local.address.socketKey().network("udp"), remote.string())).(*net.UDPConn)

	throw(conn.SetReadBuffer(1 << 20))

	return conn
}
