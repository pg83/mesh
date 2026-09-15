package main

import (
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

func udpGuard(key SocketKey) io.Closer {
	path := filepath.Join(os.TempDir(), "mesh-"+key.network("udp")+"-"+key.addr+"-"+strconv.Itoa(int(key.port))+".lock")
	f := throw2(os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600))

	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		throw(err)
	}

	return f
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

func (u *UDPWriter) writePacket(packet []byte) {
	throw2(u.conn.WriteToUDP(packet, u.remote))
}

func socketInterface(fd, iface int, v6 bool) error {
	if v6 {
		return unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF, iface)
	}

	return unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_BOUND_IF, iface)
}

func routeViable(iface int, ip netip.Addr) bool {
	dialer := net.Dialer{Control: udpControl(iface)}
	conn, err := dialer.Dial("udp", net.JoinHostPort(ip.String(), "9"))

	if err == nil {
		conn.Close()
	}

	return err == nil
}
