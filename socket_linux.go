package main

import (
	"cmp"
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

// Whether this interface has a route to that address, and whether the question
// could be answered at all. A kernel that will not show its routing table is
// saying nothing about the route, which is not the same as saying there is
// none.
func routeViable(iface int, ip netip.Addr) (bool, error) {
	routes, err := netlink.RouteList(nil, unix.AF_INET)

	if failed := cmp.Or(err, sys.check("routes")); failed != nil {
		return false, failed
	}

	for _, route := range routes {
		if route.LinkIndex == iface && (route.Dst == nil || route.Dst.Contains(ip.AsSlice())) {
			return true, nil
		}
	}

	return false, nil
}
