package main

import (
	"encoding/binary"
	"net"
)

func ipDestination(packet []byte) net.IP {
	if validIPv4(packet) {
		return net.IP(packet[16:20])
	}

	if len(packet) >= 40 && packet[0]>>4 == 6 && int(binary.BigEndian.Uint16(packet[4:6]))+40 == len(packet) {
		return net.IP(packet[24:40])
	}

	return nil
}

func validIPv4(packet []byte) bool {
	if len(packet) < 20 || packet[0]>>4 != 4 {
		return false
	}

	head := int(packet[0]&15) * 4

	return head >= 20 && head <= len(packet) && int(binary.BigEndian.Uint16(packet[2:4])) == len(packet)
}
