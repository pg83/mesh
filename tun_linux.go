package main

import (
	"errors"
	"net"
	"net/netip"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const defaultTun = "mesh0"

type Tun = LinuxTun

type LinuxTun struct {
	fd   int
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

	return &LinuxTun{fd: fd, link: link}
}

func (t *LinuxTun) route(prefix netip.Prefix) {
	throw(netlink.RouteReplace(&netlink.Route{LinkIndex: t.link.Attrs().Index, Dst: prefixNet(prefix), Scope: netlink.SCOPE_LINK}))
}

// A read of the device is a raw system call, and a goroutine of this program is
// interrupted by a signal often. That is the one failure worth another go; any
// other means the device is gone, and there is nothing to wait for.
func (t *LinuxTun) read(buf []byte) []byte {
	n := 0

	for {
		var err error

		if err = sys.check("tun read"); err == nil {
			n, err = unix.Read(t.fd, buf)
		}

		if errors.Is(err, unix.EINTR) {
			continue
		}

		throw(err)

		break
	}

	return buf[:n]
}

func (t *LinuxTun) write(packet []byte) {
	if ipDestination(packet) == nil {
		return
	}

	for {
		var err error

		if err = sys.check("tun write"); err == nil {
			_, err = unix.Write(t.fd, packet)
		}

		if errors.Is(err, unix.EINTR) {
			continue
		}

		throw(err)

		return
	}
}
