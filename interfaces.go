package main

import (
	"net/netip"
	"os"

	"golang.org/x/sys/unix"
)

type InterfaceAddress struct {
	ip     netip.Addr
	prefix netip.Prefix
	iface  int
}

type InterfaceState []InterfaceAddress

func interfaceFile(fd int) *os.File {
	unix.CloseOnExec(fd)
	throw(unix.SetNonblock(fd, true))

	return os.NewFile(uintptr(fd), "interface events")
}
