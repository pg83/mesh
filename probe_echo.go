//go:build meshprobe

package main

import (
	"encoding/binary"
	"encoding/json"
	"net"
	"time"
)

func echoProbe(path, _ string, index uint16) {
	cfg := loadConfig(path)
	reg := newRegistry(cfg.Registry, cfg.RegistryVersion)
	me, peer := reg.byIndex[cfg.Index], reg.byIndex[index]
	key := deriveKey(decodeKey(cfg.Key))
	session := newSession(me, peer, key.private)
	conn := throw2(net.ListenUDP(cfg.Endpoint[0].description().socketKey().network("udp"), cfg.Endpoint[0].binding()))
	mine := cfg.Endpoint[0].description().vertex()
	clients := map[SocketAddress]Vertex{}
	buf := make([]byte, maxPacket)
	next := time.Time{}
	id := uint64(time.Now().UnixNano())

	send := func(target *net.UDPAddr, inner []byte) {
		id++
		throw2(conn.WriteToUDP(session.seal(inner, id), target))
	}

	for {
		if time.Now().After(next) {
			for target, other := range clients {
				edges := []Edge{{From: me.vertex().hash(), To: mine.hash()}, {From: mine.hash(), To: me.vertex().hash()}, {From: other.hash(), To: mine.hash()}}
				updates := EdgeRecords{}

				for _, edge := range edges {
					updates = append(updates, Update{Edge: edge, State: State{ID: uint64(time.Now().UnixNano()), Alive: true}})
				}

				send(target.addr(), encodeVertices(VertexRecords{me.vertex(), mine, other}))
				send(target.addr(), encodeEdges(updates))
			}

			next = time.Now().Add(200 * time.Millisecond)
		}

		conn.SetReadDeadline(next)

		size, remote, err := conn.ReadFromUDP(buf)

		if err != nil {
			continue
		}

		inner, ok := session.open(buf[:size])

		if !ok || len(inner) == 0 {
			continue
		}

		if inner[0] == innerBinding {
			var binding Binding

			if json.Unmarshal(inner[1:], &binding) == nil && !binding.Reply && binding.To.hash() == mine.hash() && binding.From.Node == peer.index {
				clients[socketAddress(remote.IP, remote.Port)] = binding.From
				send(remote, bindingInner(Binding{From: mine, To: binding.From, Reply: true}))
			}

			continue
		}

		other, known := clients[socketAddress(remote.IP, remote.Port)]

		if inner[0] != innerData || !known {
			continue
		}

		data, ok := decodeData(inner)

		if !ok || !validIPv4(data.ip) || data.ip[9] != 1 {
			continue
		}

		packet := append([]byte(nil), data.ip...)
		head := int(packet[0]&15) * 4

		if len(packet) < head+8 || packet[head] != 8 {
			continue
		}

		copy(packet[12:16], data.ip[16:20])
		copy(packet[16:20], data.ip[12:16])
		packet[head] = 0
		packet[10], packet[11], packet[head+2], packet[head+3] = 0, 0, 0, 0
		binary.BigEndian.PutUint16(packet[10:12], internetChecksum(packet[:head]))
		binary.BigEndian.PutUint16(packet[head+2:head+4], internetChecksum(packet[head:]))
		send(remote, encodeData(&Data{path: []Edge{{From: mine.hash(), To: other.hash()}}, ip: packet}))
	}
}

func internetChecksum(packet []byte) uint16 {
	sum := uint32(0)

	for len(packet) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(packet))
		packet = packet[2:]
	}

	if len(packet) != 0 {
		sum += uint32(packet[0]) << 8
	}

	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}

	return ^uint16(sum)
}
