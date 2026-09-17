package main

import (
	"context"
	"encoding/binary"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
)

const (
	stackNIC   = 1
	stackQueue = 256
)

type Netstack struct {
	node      *Node
	stack     *stack.Stack
	link      *channel.Endpoint
	addresses [][4]byte
	services  map[Service]bool
}

type Service struct {
	proto byte
	port  uint16
}

func newNetstack(n *Node, mtu int, addresses [][4]byte) *Netstack {
	s := &Netstack{node: n, addresses: addresses, services: map[Service]bool{}}

	s.stack = stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})

	s.link = channel.New(stackQueue, uint32(mtu), "")

	netstackCheck("nic", s.stack.CreateNIC(stackNIC, s.link))

	for _, ip := range addresses {
		address := tcpip.ProtocolAddress{Protocol: ipv4.ProtocolNumber, AddressWithPrefix: tcpip.AddrFrom4(ip).WithPrefix()}

		netstackCheck("address", s.stack.AddProtocolAddress(stackNIC, address, stack.AddressProperties{}))
	}

	s.stack.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: stackNIC}})
	go n.loop("netstack egress", s.egress)

	return s
}

func netstackCheck(what string, err tcpip.Error) {
	if err == nil && sys.check("netstack") != nil {
		err = &tcpip.ErrAborted{}
	}

	if err != nil {
		throwFmt("netstack %s: %v", what, err)
	}
}

func (s *Netstack) register(proto byte, port uint16) {
	s.services[Service{proto: proto, port: port}] = true
}

func (s *Netstack) accepts(packet []byte) bool {
	if !validIPv4(packet) {
		return false
	}

	destination := [4]byte(packet[16:20])
	known := false

	for _, ip := range s.addresses {
		known = known || ip == destination
	}

	head := int(packet[0]&15) * 4

	if !known || len(packet) < head+4 {
		return false
	}

	return s.services[Service{proto: packet[9], port: binary.BigEndian.Uint16(packet[head+2:])}]
}

func (s *Netstack) inject(packet []byte) {
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(append([]byte(nil), packet...))})

	s.link.InjectInbound(ipv4.ProtocolNumber, pkt)
	pkt.DecRef()
}

func (s *Netstack) egress() {
	for {
		pkt := s.link.ReadContext(context.Background())
		data := pkt.ToBuffer()
		packet := data.Flatten()

		pkt.DecRef()

		if destination := ipDestination(packet); destination != nil {
			post(s.node.tunInbox.in, any(TunPacket{payload: packet, destination: destination}))
		}
	}
}
