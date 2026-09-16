package main

import (
	"net"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

type UDPWriter = LinuxUDPWriter

type LinuxUDPWriter struct {
	conn   *net.UDPConn
	local  *LocalAddress
	remote *net.UDPAddr
}

func (u *LinuxUDPWriter) writePacket(packet []byte) {
	if u.local.address.ipv6() {
		control := &ipv6.ControlMessage{Src: u.local.address.ip(), IfIndex: u.local.iface}

		throw3(u.conn.WriteMsgUDP(packet, control.Marshal(), u.remote))

		return
	}

	control := &ipv4.ControlMessage{Src: u.local.address.ip(), IfIndex: u.local.iface}

	throw3(u.conn.WriteMsgUDP(packet, control.Marshal(), u.remote))
}
