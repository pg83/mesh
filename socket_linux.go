package main

import (
	"golang.org/x/net/ipv4"
	"golang.org/x/sys/unix"
	"net"
	"strconv"
)

func udpGuard(port uint16) net.Listener {
	return throw2(net.Listen("unix", "@mesh-udp-"+strconv.Itoa(int(port))))
}

func socketReuse(fd, iface int) error {
	return unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
}

func (u *UDPLink) write(packet []byte) {
	control := &ipv4.ControlMessage{Src: u.local.address.ip(), IfIndex: u.local.iface}

	u.conn.WriteMsgUDP(packet, control.Marshal(), nil)
}
