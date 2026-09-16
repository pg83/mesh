package main

import (
	"hash/fnv"
	"net"
	"net/netip"
	"slices"
	"strconv"
)

type ExitRoute struct {
	prefix netip.Prefix
	nodes  []uint16
}

func parseRoutes(cfg *Config) []ExitRoute {
	routes := []ExitRoute{}

	for text, names := range cfg.Routes {
		prefix, err := netip.ParsePrefix(text)

		if err != nil || !prefix.Addr().Is4() {
			throwFmt("routes: bad IPv4 prefix %q", text)
		}

		route := ExitRoute{prefix: prefix.Masked()}

		for _, name := range names {
			index := uint16(0)

			for _, peer := range cfg.Registry {
				if peer.Name == name || strconv.Itoa(int(peer.Index)) == name {
					index = peer.Index
				}
			}

			if index == 0 {
				throwFmt("routes: %s: unknown node %q", text, name)
			}

			route.nodes = append(route.nodes, index)
		}

		if len(route.nodes) == 0 {
			throwFmt("routes: %s: no exit nodes", text)
		}

		slices.Sort(route.nodes)
		routes = append(routes, route)
	}

	slices.SortFunc(routes, func(a, b ExitRoute) int { return b.prefix.Bits() - a.prefix.Bits() })

	return routes
}

func routeHalves(prefix netip.Prefix) []netip.Prefix {
	if prefix.Bits() == 0 {
		return []netip.Prefix{netip.MustParsePrefix("0.0.0.0/1"), netip.MustParsePrefix("128.0.0.0/1")}
	}

	return []netip.Prefix{prefix}
}

func prefixNet(prefix netip.Prefix) *net.IPNet {
	return &net.IPNet{IP: prefix.Addr().AsSlice(), Mask: net.CIDRMask(prefix.Bits(), prefix.Addr().BitLen())}
}

func flowHash(packet []byte) uint32 {
	h := fnv.New32a()
	head := int(packet[0]&15) * 4

	h.Write(packet[9:10])
	h.Write(packet[12:20])

	if (packet[9] == 6 || packet[9] == 17) && len(packet) >= head+4 {
		h.Write(packet[head : head+4])
	}

	return h.Sum32()
}
