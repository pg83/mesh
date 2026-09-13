package main

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func openInterfaceEvents() *os.File {
	fd := throw2(unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW, unix.NETLINK_ROUTE))

	throw(unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK,
		Groups: unix.RTMGRP_LINK | unix.RTMGRP_IPV4_IFADDR | unix.RTMGRP_IPV6_IFADDR}))

	return interfaceFile(fd)
}

func interfaceEvent(data []byte) bool {
	messages, err := syscall.ParseNetlinkMessage(data)

	if err != nil {
		return true
	}

	for _, msg := range messages {
		switch msg.Header.Type {
		case unix.RTM_NEWLINK, unix.RTM_DELLINK, unix.RTM_NEWADDR, unix.RTM_DELADDR, unix.NLMSG_OVERRUN:
			return true
		}
	}

	return false
}
