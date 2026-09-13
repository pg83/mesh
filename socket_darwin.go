package main

import (
	"golang.org/x/sys/unix"
	"net"
	"strconv"
)

func udpGuard(key SocketKey) net.Listener {
	addr := "127.0.0.1"

	if key.ipv6 {
		addr = "::1"
	}

	return throw2(net.Listen(key.network("tcp"), net.JoinHostPort(addr, strconv.Itoa(int(key.port)))))
}

func socketReuse(fd, iface int, v6 bool) error {
	return try(func() {
		throw(unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1))
		throw(unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEPORT, 1))

		if iface != 0 {
			if v6 {
				throw(unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF, iface))
			} else {
				throw(unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_BOUND_IF, iface))
			}
		}
	}).asError()
}

func (u *UDPLink) write(packet []byte) {
	u.conn.Write(packet)
}
