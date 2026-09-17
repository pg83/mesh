package main

import (
	"net"
	"net/netip"
	"os"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const defaultTun = "mesh0"

type Tun = LinuxTun

type LinuxTun struct {
	// Through a file rather than the descriptor: a read of a device is
	// interrupted by signals often enough that the standard library keeps a
	// loop for it, and there is no reason to write that loop again here.
	file *os.File
	link netlink.Link
}

func openTun(name string, intip [4]byte, subnet string, mtu int) *LinuxTun {
	fd := throw2(unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0))
	ifr := throw2(unix.NewIfreq(name))

	ifr.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI)
	throw(unix.IoctlIfreq(fd, unix.TUNSETIFF, ifr))

	_, ipnet := throw3(net.ParseCIDR(subnet))

	ipnet.IP = net.IP(intip[:])

	link := throw2(netlink.LinkByName(name))

	throw(netlink.AddrReplace(link, &netlink.Addr{IPNet: ipnet}))
	throw(netlink.LinkSetMTU(link, mtu))
	throw(netlink.LinkSetUp(link))
	throw(unix.IoctlSetInt(fd, unix.TUNSETPERSIST, 1))

	return &LinuxTun{file: os.NewFile(uintptr(fd), name), link: link}
}

func (t *LinuxTun) route(prefix netip.Prefix) {
	throw(netlink.RouteReplace(&netlink.Route{LinkIndex: t.link.Attrs().Index, Dst: prefixNet(prefix), Scope: netlink.SCOPE_LINK}))
}

func (t *LinuxTun) read(buf []byte) []byte {
	throw(sys.check("tun read"))

	return buf[:throw2(t.file.Read(buf))]
}

func (t *LinuxTun) write(packet []byte) {
	if ipDestination(packet) == nil {
		return
	}

	throw(sys.check("tun write"))
	throw2(t.file.Write(packet))
}
