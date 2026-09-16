package main

import (
	"cmp"
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
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

func udpControl(iface int) func(string, string, syscall.RawConn) error {
	return func(network, address string, raw syscall.RawConn) error {
		var result error

		err := raw.Control(func(fd uintptr) { result = socketReuse(int(fd), iface, strings.HasSuffix(network, "6")) })

		return errors.Join(err, result)
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

func newUDPSocket(key SocketKey, implicit bool) *UDPSocket {
	var guard io.Closer

	if !implicit {
		guard = udpGuard(key)
	}

	lc := net.ListenConfig{Control: udpControl(0)}
	udp := throw2(lc.ListenPacket(context.Background(), key.network("udp"), net.JoinHostPort(key.addr, strconv.Itoa(int(key.port))))).(*net.UDPConn)

	throw(udp.SetReadBuffer(1 << 20))

	socket := &UDPSocket{conn: udp, port: uint16(udp.LocalAddr().(*net.UDPAddr).Port), guard: guard, implicit: implicit}

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
	Addr netip.Addr `json:"addr"`
	Port uint16     `json:"port"`
}

func compareSocketAddress(a, b SocketAddress) int {
	if v := a.Addr.Compare(b.Addr); v != 0 {
		return v
	}

	return cmp.Compare(a.Port, b.Port)
}

func (s SocketAddress) udpAddr() *net.UDPAddr {
	return net.UDPAddrFromAddrPort(netip.AddrPortFrom(s.Addr, s.Port))
}

func (s SocketAddress) vertex() Vertex {
	return udpVertex(s.ip(), int(s.Port))
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

		return errors.Join(err, result)
	}
}

type UDPSocket struct {
	guard    io.Closer
	conn     *net.UDPConn
	read     func([]byte) (int, net.IP, net.Addr, error)
	port     uint16
	implicit bool
}

type SocketKey struct {
	addr string
	port uint16
	ipv6 bool
}
