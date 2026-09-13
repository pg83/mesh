package main

import (
	"net"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const defaultTun = "mesh0"

type Tun struct {
	fd int
}

func openTun(name string, intip [4]byte, subnet string, mtu int) *Tun {
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

	return &Tun{fd: fd}
}

func (t *Tun) read(buf []byte) []byte {
	n := throw2(unix.Read(t.fd, buf))

	return buf[:n]
}

func (t *Tun) write(packet []byte) {
	throw2(unix.Write(t.fd, packet))
}
