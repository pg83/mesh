package main

import (
	"net"
	"net/netip"
	"strconv"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func udpGuard(key SocketKey) net.Listener {
	return throw2(net.Listen("unix", "@mesh-"+key.network("udp")+"-"+key.addr+"-"+strconv.Itoa(int(key.port))))
}

func socketReuse(fd, iface int, v6 bool) error {
	return unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
}

func socketInterface(fd, iface int, v6 bool) error {
	return try(func() {
		device := throw2(net.InterfaceByIndex(iface))

		throw(unix.SetsockoptString(fd, unix.SOL_SOCKET, unix.SO_BINDTODEVICE, device.Name))
	}).asError()
}

func routeViable(iface int, ip netip.Addr) bool {
	routes, err := netlink.RouteList(nil, unix.AF_INET)

	if err != nil {
		return false
	}

	for _, route := range routes {
		if route.LinkIndex == iface && (route.Dst == nil || route.Dst.Contains(ip.AsSlice())) {
			return true
		}
	}

	return false
}
