package main

import (
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"golang.org/x/sys/unix"
	"net"
	"strconv"
	"syscall"
)

func udpClientControl(iface int) func(string, string, syscall.RawConn) error {
	return nil
}

func udpGuard(key SocketKey) net.Listener {
	return throw2(net.Listen("unix", "@mesh-"+key.network("udp")+"-"+key.addr+"-"+strconv.Itoa(int(key.port))))
}

func socketReuse(fd, iface int, v6 bool) error {
	return unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
}

func (u *UDPWriter) writePacket(packet []byte) {
	if u.local.address.ipv6() {
		control := &ipv6.ControlMessage{Src: u.local.address.ip(), IfIndex: u.local.iface}

		throw3(u.conn.WriteMsgUDP(packet, control.Marshal(), u.remote))

		return
	}

	control := &ipv4.ControlMessage{Src: u.local.address.ip(), IfIndex: u.local.iface}

	throw3(u.conn.WriteMsgUDP(packet, control.Marshal(), u.remote))
}

func socketInterface(fd, iface int, v6 bool) error {
	return try(func() {
		device := throw2(net.InterfaceByIndex(iface))

		throw(unix.SetsockoptString(fd, unix.SOL_SOCKET, unix.SO_BINDTODEVICE, device.Name))
	}).asError()
}
