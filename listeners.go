package main

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"slices"
)

func (n *Node) implicitSocket(ip netip.Addr) *SocketKey {
	for key, socket := range n.sockets {
		if socket.implicit && key.addr == ip.String() {
			return &key
		}
	}

	return nil
}

func (n *Node) bindings() []ListenerBinding {
	out := slices.Clone(n.endpoints)

	for key, socket := range n.sockets {
		if socket.implicit {
			ip := netip.MustParseAddr(key.addr)
			bind := SocketAddress{Addr: ip, Port: key.port}

			out = append(out, ListenerBinding{public: Endpoint{Proto: "udp", Addr: ip.String(), Port: key.port}, bind: bind})
		}
	}

	return out
}

func (n *Node) udpSocketFor(wire SocketAddress) *UDPSocket {
	if socket := n.sockets[wire.socketKey()]; socket != nil {
		return socket
	}

	key := wire.socketKey()

	key.addr = key.wildcard()

	return n.sockets[key]
}

type UDPSource struct {
	socket *UDPSocket
	local  *LocalAddress
	vertex Vertex
}

func (n *Node) udpSource(src InterfaceAddress) *UDPSource {
	var best *UDPSource

	for id, local := range n.local {
		vertex := n.addresses[id]

		if !local.receive || vertex.Proto != "udp" || !vertex.isEndpoint() || local.address.Addr != src.ip || local.iface != src.iface {
			continue
		}

		socket := n.udpSocketFor(local.address)

		if socket == nil {
			continue
		}

		if best == nil || (best.socket.implicit && !socket.implicit) || (best.socket.implicit == socket.implicit && compareVertex(vertex, best.vertex) < 0) {
			best = &UDPSource{socket: socket, local: local, vertex: vertex}
		}
	}

	return best
}

func (n *Node) syncListeners(addresses InterfaceState) {
	present := map[string]bool{}

	for _, addr := range addresses {
		present[addr.ip.String()] = true
	}

	udp := map[SocketKey]bool{}
	ws := map[string]bool{}
	wildcard := map[bool]bool{}
	concrete := map[netip.Addr]bool{}
	configs := slices.Clone(n.endpoints)

	slices.SortStableFunc(configs, func(a, b ListenerBinding) int {
		if a.bind.ip().IsUnspecified() && !b.bind.ip().IsUnspecified() {
			return -1
		}

		if !a.bind.ip().IsUnspecified() && b.bind.ip().IsUnspecified() {
			return 1
		}

		return 0
	})

	for _, config := range configs {
		bind := config.bind

		if !bind.ip().IsUnspecified() && !present[bind.Addr.String()] {
			continue
		}

		if config.public.Proto == "udp" {
			key := bind.socketKey()
			any := key

			any.addr = key.wildcard()

			if udp[any] {
				continue
			}

			udp[key] = true

			if bind.ip().IsUnspecified() {
				wildcard[bind.ipv6()] = true
			} else {
				concrete[bind.Addr] = true
			}

			if n.sockets[key] == nil {
				socket := newUDPSocket(key, false)

				n.sockets[key] = socket
				go n.loop("UDP listener", func() { n.discoverUDP(socket) })
			}
		} else {
			ws[n.listenWS(config)] = true
		}
	}

	for _, addr := range addresses {
		if addr.ip.IsLoopback() || wildcard[addr.ip.Is6()] || concrete[addr.ip] {
			continue
		}

		key := n.implicitSocket(addr.ip)

		if key == nil {
			try(func() {
				socket := newUDPSocket(SocketKey{addr: addr.ip.String(), ipv6: addr.ip.Is6()}, true)

				key = &SocketKey{addr: addr.ip.String(), port: socket.port, ipv6: addr.ip.Is6()}
				n.sockets[*key] = socket
				go n.loop("UDP listener", func() { n.discoverUDP(socket) })
			}).catch(func(e *Exception) { n.log.Debug("implicit UDP socket failed", "address", addr.ip.String(), "err", e) })
		}

		if key != nil {
			udp[*key] = true
		}
	}

	for key, socket := range n.sockets {
		if !udp[key] {
			socket.conn.Close()

			if socket.guard != nil {
				socket.guard.Close()
			}

			delete(n.sockets, key)
		}
	}

	for addr, listener := range n.listeners {
		if !ws[addr] {
			listener.server.Close()
			delete(n.listeners, addr)

			continue
		}

		if !listener.started {
			listener.started = true
			go n.loop("WS listener", func() {
				err := listener.server.Serve(listener.conn)

				if !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
					throw(err)
				}
			})
		}
	}
}
