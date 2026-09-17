package main

import (
	"net"
)

type UDPWriter = DarwinUDPWriter

type DarwinUDPWriter struct {
	conn   *net.UDPConn
	local  *LocalAddress
	remote *net.UDPAddr
}

func (u *DarwinUDPWriter) writePacket(packet []byte) {
	throw(sys.check("udp write"))
	throw2(u.conn.WriteToUDP(packet, u.remote))
}
