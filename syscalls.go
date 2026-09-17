package main

import (
	"context"
	"net"
	"os"
)

// Everything the node asks of the operating system that the operating system
// is entitled to refuse goes through here. The ordinary build passes each call
// straight on. The chaos build answers some of them with an error the kernel
// would have been within its rights to return, which is the only way the
// recovery paths are ever walked.
//
// Two shapes. A call that hands back an error takes the object it works on and
// returns what the real one returns, so the caller cannot tell the difference.
// A call that raises the failure itself asks check for a verdict and throws it.
type Syscalls interface {
	interfaces() ([]net.Interface, error)
	addresses(iface net.Interface) ([]net.Addr, error)
	listenPacket(config net.ListenConfig, network, address string) (net.PacketConn, error)
	readSocket(socket *UDPSocket, buf []byte) (int, net.IP, net.Addr, error)
	interfaceEvent(socket *os.File, buf []byte) error
	accepts(listener net.Listener) net.Listener
	check(what string) error
}

type OS struct{}

func (OS) interfaces() ([]net.Interface, error) {
	return net.Interfaces()
}

func (OS) addresses(iface net.Interface) ([]net.Addr, error) {
	return iface.Addrs()
}

func (OS) listenPacket(config net.ListenConfig, network, address string) (net.PacketConn, error) {
	return config.ListenPacket(context.Background(), network, address)
}

func (OS) readSocket(socket *UDPSocket, buf []byte) (int, net.IP, net.Addr, error) {
	return socket.read(buf)
}

func (OS) interfaceEvent(socket *os.File, buf []byte) error {
	return readInterfaceEvent(socket, buf)
}

func (OS) accepts(listener net.Listener) net.Listener {
	return listener
}

func (OS) check(string) error {
	return nil
}
