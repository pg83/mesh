package main

import (
	"os"

	"golang.org/x/sys/unix"
)

func openInterfaceEvents() *os.File {
	fd := throw2(unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW, unix.NETLINK_ROUTE))

	throw(unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK,
		Groups: unix.RTMGRP_LINK | unix.RTMGRP_IPV4_IFADDR | unix.RTMGRP_IPV6_IFADDR}))

	return interfaceFile(fd)
}

func interfaceEvent([]byte) bool {
	return true
}
