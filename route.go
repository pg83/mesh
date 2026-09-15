package main

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"time"
)

const routeTTL = 30 * time.Second

type Route struct {
	host    string
	targets []netip.Addr
	viable  map[int]bool
	at      time.Time
	pending bool
}

func (n *Node) reachable(src InterfaceAddress, target Vertex) bool {
	route := n.routeCache[target.Addr]

	if route == nil || (!route.pending && time.Since(route.at) >= routeTTL) {
		if route == nil {
			route = &Route{host: target.Addr}
			n.routeCache[target.Addr] = route
		}

		route.pending = true

		ifaces := []int{}

		for _, iface := range n.interfaces {
			ifaces = append(ifaces, iface.iface)
		}

		go n.resolveRoute(target.Addr, slices.Compact(slices.Sorted(slices.Values(ifaces))))
	}

	if route.at.IsZero() {
		return true
	}

	for _, ip := range route.targets {
		if src.prefix.Contains(ip) {
			return true
		}
	}

	return route.viable[src.iface]
}

func (n *Node) resolveRoute(host string, ifaces []int) {
	route := &Route{host: host, at: time.Now(), viable: map[int]bool{}}

	if ip, err := netip.ParseAddr(host); err == nil {
		route.targets = []netip.Addr{ip.Unmap()}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

		defer cancel()

		if ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host); err == nil {
			for _, ip := range ips {
				route.targets = append(route.targets, ip.Unmap())
			}
		}
	}

	for _, iface := range ifaces {
		for _, ip := range route.targets {
			if routeViable(iface, ip) {
				route.viable[iface] = true
			}
		}
	}

	post(n.events.in, any(route))
}
