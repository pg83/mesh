package main

import (
	"context"
	"net"
	"net/netip"
	"time"
)

const routeTTL = 30 * time.Second

type Route struct {
	host    string
	targets []netip.Addr
	sources []netip.Addr
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

		go n.resolveRoute(target.Addr)
	}

	if route.at.IsZero() {
		return true
	}

	for _, ip := range route.targets {
		if src.prefix.Contains(ip) {
			return true
		}
	}

	for _, ip := range route.sources {
		if ip == src.ip {
			return true
		}
	}

	return false
}

func (n *Node) resolveRoute(host string) {
	route := &Route{host: host, at: time.Now()}

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

	for _, ip := range route.targets {
		if conn, err := net.Dial("udp", net.JoinHostPort(ip.String(), "9")); err == nil {
			source, _ := netip.AddrFromSlice(conn.LocalAddr().(*net.UDPAddr).IP)

			route.sources = append(route.sources, source.Unmap())
			conn.Close()
		}
	}

	post(n.events.in, any(route))
}
