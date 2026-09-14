package main

import (
	"context"
	"errors"
	"math/rand/v2"
	"net"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	socketQuarantine = time.Minute
	socketPortFirst  = 49152
	socketPortCount  = 65536 - socketPortFirst
)

type PoolKey struct {
	proto   string
	address SocketAddress
}

type PoolSocket struct {
	key   PoolKey
	until time.Time
	used  bool
}

type SocketPool struct {
	mu      sync.Mutex
	sockets map[PoolKey]*PoolSocket
}

func (p *SocketPool) acquire(local SocketAddress, proto string, bind func(SocketAddress) error) *PoolSocket {
	p.mu.Lock()

	defer p.mu.Unlock()

	now := time.Now()

	if p.sockets == nil {
		p.sockets = map[PoolKey]*PoolSocket{}
	}

	for key, socket := range p.sockets {
		if !socket.until.IsZero() && !now.Before(socket.until) {
			delete(p.sockets, key)
		}
	}

	start, step := rand.IntN(socketPortCount), 2*rand.IntN(socketPortCount/2)+1

	for i := 0; i < socketPortCount; i++ {
		local.Port = uint16(socketPortFirst + (start+i*step)%socketPortCount)

		key := PoolKey{proto: proto, address: local}

		if p.sockets[key] != nil {
			continue
		}

		socket := &PoolSocket{key: key}
		err := bind(local)

		if errors.Is(err, unix.EADDRINUSE) {
			socket.until = now.Add(socketQuarantine)
			p.sockets[key] = socket

			continue
		}

		throw(err)
		socket.used = true
		p.sockets[key] = socket

		return socket
	}

	throwFmt("no available %s source port on %s", proto, local.Addr)

	return nil
}

func (p *SocketPool) retired() map[uint64]bool {
	p.mu.Lock()

	defer p.mu.Unlock()

	vertices := map[uint64]bool{}
	now := time.Now()

	for key, socket := range p.sockets {
		if socket.used && !socket.until.IsZero() && now.Before(socket.until) {
			vertex := Vertex{Proto: key.proto, Addr: key.address.Addr.String(), Port: key.address.Port}

			vertices[vertex.hash()] = true
		}
	}

	return vertices
}

func (p *SocketPool) release(socket *PoolSocket, closeSocket func() error) error {
	p.mu.Lock()

	defer p.mu.Unlock()

	if p.sockets[socket.key] != socket || !socket.until.IsZero() {
		return nil
	}

	var err error

	if closeSocket != nil {
		err = closeSocket()
	}

	socket.until = time.Now().Add(socketQuarantine)

	return err
}

func (p *SocketPool) udp(local *LocalAddress) (*net.UDPConn, *PoolSocket) {
	var conn net.PacketConn

	config := net.ListenConfig{Control: udpClientControl(local.iface)}

	socket := p.acquire(local.address, "udp", func(address SocketAddress) error {
		var err error
		conn, err = config.ListenPacket(context.Background(), address.socketKey().network("udp"), address.string())

		return err
	})

	return conn.(*net.UDPConn), socket
}

func bindSocket(fd int, address SocketAddress) error {
	if address.ipv6() {
		return unix.Bind(fd, &unix.SockaddrInet6{Addr: address.Addr.As16(), Port: int(address.Port)})
	}

	return unix.Bind(fd, &unix.SockaddrInet4{Addr: address.Addr.As4(), Port: int(address.Port)})
}

type PoolConn struct {
	net.Conn
	pool   *SocketPool
	socket *PoolSocket
}

func (c *PoolConn) close() error {
	return c.pool.release(c.socket, c.Conn.Close)
}

func (c *PoolConn) Close() error {
	return c.close()
}

func (p *SocketPool) tcp(ctx context.Context, local *LocalAddress, remote string) (net.Conn, error) {
	var conn net.Conn
	var sockets []*PoolSocket

	err := try(func() {
		dialer := &net.Dialer{Control: func(network, address string, raw syscall.RawConn) error {
			return try(func() {
				throw(raw.Control(func(fd uintptr) {
					throw(socketInterface(int(fd), local.iface, local.address.ipv6()))

					socket := p.acquire(local.address, "tcp", func(address SocketAddress) error { return bindSocket(int(fd), address) })

					sockets = append(sockets, socket)
				}))
			}).asError()
		}}

		conn = throw2(dialer.DialContext(ctx, local.address.socketKey().network("tcp"), remote))
	})

	var result net.Conn

	for _, socket := range sockets {
		if conn != nil && socket.key.address == socketAddress(conn.LocalAddr().(*net.TCPAddr).IP, conn.LocalAddr().(*net.TCPAddr).Port) {
			result = &PoolConn{Conn: conn, pool: p, socket: socket}
		} else {
			p.release(socket, nil)
		}
	}

	return result, err.asError()
}
