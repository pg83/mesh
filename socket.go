package main

import (
	"context"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

type ListenerBinding struct {
	config EndpointConfig
	public Endpoint
	bind   SocketAddress
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

		return udpVertex(a.IP, a.Port).endpoint()
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
	udp := throw2(lc.ListenPacket(context.Background(), key.network("udp"), net.JoinHostPort(key.addr, strconv.Itoa(int(key.port))))).(*net.UDPConn)

	throw(udp.SetReadBuffer(1 << 20))

	socket := &UDPSocket{conn: udp, port: key.port, guard: guard}

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

type SocketAddress struct {
	Addr netip.Addr
	Port uint16
}

func socketAddress(ip net.IP, port int) SocketAddress {
	addr, _ := netip.AddrFromSlice(ip)

	return SocketAddress{Addr: addr.Unmap(), Port: uint16(port)}
}

func (s SocketAddress) ip() net.IP {
	return net.IP(s.Addr.AsSlice())
}

func (s SocketAddress) ipv6() bool {
	return s.Addr.Is6()
}

func (s SocketAddress) addr() *net.UDPAddr {
	return &net.UDPAddr{IP: s.ip(), Port: int(s.Port)}
}

func (s SocketAddress) socketKey() SocketKey {
	return SocketKey{addr: s.Addr.String(), port: s.Port, ipv6: s.ipv6()}
}

func (s SocketAddress) string() string {
	return net.JoinHostPort(s.Addr.String(), strconv.Itoa(int(s.Port)))
}

func tcpControl(iface int) func(string, string, syscall.RawConn) error {
	return func(network, address string, raw syscall.RawConn) error {
		var result error

		err := raw.Control(func(fd uintptr) { result = socketInterface(int(fd), iface, strings.HasSuffix(network, "6")) })

		if err != nil {
			result = err
		}

		return result
	}
}
