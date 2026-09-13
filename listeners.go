package main

import (
	"errors"
	"net"
	"net/http"
	"slices"
)

func (n *Node) syncListeners(addresses InterfaceState) {
	present := map[string]bool{}

	for _, addr := range addresses {
		present[addr.ip.String()] = true
	}

	udp := map[SocketKey]bool{}
	ws := map[string]bool{}
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

			udp[key] = true

			if n.sockets[key] == nil {
				socket := newUDPSocket(key)

				n.sockets[key] = socket
				go n.loop("UDP listener", func() { n.discoverUDP(socket) })
			}
		} else {
			ws[n.listenWS(config)] = true
		}
	}

	for key, socket := range n.sockets {
		if !udp[key] {
			socket.conn.Close()
			socket.guard.Close()
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
