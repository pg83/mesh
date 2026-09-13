package main

import (
	"cmp"
	"net"
	"net/netip"
	"os"
	"slices"
	"time"

	"golang.org/x/sys/unix"
)

type InterfaceAddress struct {
	ip    netip.Addr
	iface int
}

type InterfaceState []InterfaceAddress

func interfaceFile(fd int) *os.File {
	unix.CloseOnExec(fd)
	throw(unix.SetNonblock(fd, true))

	return os.NewFile(uintptr(fd), "interface events")
}

func (n *Node) interfaceAddresses() (InterfaceState, error) {
	interfaces, err := net.Interfaces()

	if err != nil {
		return nil, err
	}

	addresses := InterfaceState{}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Name == n.cfg.Tun {
			continue
		}

		addrs, err := iface.Addrs()

		if err != nil {
			n.log.Debug("interface addresses unavailable", "interface", iface.Name, "err", err)

			continue
		}

		for _, addr := range addrs {
			prefix, err := netip.ParsePrefix(addr.String())

			if err != nil {
				continue
			}

			ip := prefix.Addr().Unmap()

			if (!ip.IsGlobalUnicast() && !ip.IsLoopback()) || n.subnet.Contains(net.IP(ip.AsSlice())) {
				continue
			}

			addresses = append(addresses, InterfaceAddress{ip: ip, iface: iface.Index})
		}
	}

	slices.SortFunc(addresses, func(a, b InterfaceAddress) int {
		if order := cmp.Compare(a.iface, b.iface); order != 0 {
			return order
		}

		return a.ip.Compare(b.ip)
	})

	return addresses, nil
}

func (n *Node) watchInterfaces() {
	socket := openInterfaceEvents()

	defer socket.Close()

	changed := make(chan struct{}, 1)

	go n.loop("interface events", func() {
		buf := make([]byte, 65536)

		for {
			size, err := socket.Read(buf)

			if err != nil {
				n.log.Warn("interface notification lost", "err", err)
				time.Sleep(time.Second)
			} else if !interfaceEvent(buf[:size]) {
				continue
			}

			select {
			case changed <- struct{}{}:
			default:
			}
		}
	})

	ticker := time.NewTicker(30 * time.Second)

	defer ticker.Stop()

	var previous InterfaceState

	for {
		current, err := n.interfaceAddresses()

		if err != nil {
			n.log.Warn("interface scan failed", "err", err)
		} else if !slices.Equal(previous, current) {
			post(n.events.in, any(current))
			previous = current
		}

		select {
		case <-changed:
		case <-ticker.C:
		}
	}
}
