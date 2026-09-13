package main

import (
	"golang.org/x/sys/unix"
	"net"
	"strconv"
)

func udpGuard(port uint16) net.Listener {
	return throw2(net.Listen("unix", "@mesh-udp-"+strconv.Itoa(int(port))))
}

func socketReuse(fd int) error {
	return unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
}
