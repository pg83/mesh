package main

import (
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"golang.org/x/sys/unix"
	"net"
	"strconv"
)

func udpGuard(key SocketKey) net.Listener {
	return throw2(net.Listen("unix", "@mesh-"+key.network("udp")+"-"+strconv.Itoa(int(key.port))))
}

func socketReuse(fd, iface int, v6 bool) error {
	return unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
}

func (u *UDPLink) write(packet []byte) {
	if u.local.address.ipv6() {
		control := &ipv6.ControlMessage{Src: u.local.address.ip(), IfIndex: u.local.iface}

		u.conn.WriteMsgUDP(packet, control.Marshal(), nil)

		return
	}

	control := &ipv4.ControlMessage{Src: u.local.address.ip(), IfIndex: u.local.iface}

	u.conn.WriteMsgUDP(packet, control.Marshal(), nil)
}
