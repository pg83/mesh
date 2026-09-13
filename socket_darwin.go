package main

import (
	"golang.org/x/sys/unix"
	"net"
	"strconv"
)

func udpGuard(port uint16) net.Listener {
	return throw2(net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(int(port))))
}

func socketReuse(fd, iface int) error {
	return try(func() {
		throw(unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1))
		throw(unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEPORT, 1))

		if iface != 0 {
			throw(unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_BOUND_IF, iface))
		}
	}).asError()
}

func (u *UDPLink) write(packet []byte) {
	u.conn.Write(packet)
}
