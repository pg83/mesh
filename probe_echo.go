//go:build meshprobe

package main

import (
	"encoding/binary"
	"net"
	"time"
)

func echoProbe(path, destination string, index uint16) {
	cfg := loadConfig(path)
	reg := newRegistry(cfg.Registry, cfg.RegistryVersion)
	me, peer := reg.byIndex[cfg.Index], reg.byIndex[index]
	key := deriveKey(decodeKey(cfg.Key))
	session := newSession(me, peer, key.private)
	conn := throw2(net.ListenUDP("udp", cfg.Endpoint[0].binding()))
	mine := cfg.Endpoint[0].description().vertex()
	source := me.endpointID(0)
	target := parseUDPAddr(destination)
	targetPort := target.Port
	destinations := map[string]*net.UDPAddr{target.String(): target}
	clients := map[SocketAddress]uint32{}
	buf := make([]byte, maxPacket)
	next := time.Time{}
	id := uint64(time.Now().UnixNano())

	send := func(target *net.UDPAddr, kind byte, inner []byte) {
		id++
		throw2(conn.WriteToUDP(session.seal(source, kind, inner, id), target))
	}

	for {
		if time.Now().After(next) {
			record := &GraphRecord{Owner: cfg.Index, Vertices: []RecordVertex{{ID: source, Vertex: mine, Ingress: true, Egress: true}}}

			for _, other := range clients {
				record.Links = append(record.Links, Edge{From: other, To: source})
			}

			id++

			packet := compress(encodeRecord(cfg.Index, id, 0, encodeRecordBody(record)))

			for _, target := range destinations {
				send(target, kindGraph, packet)
			}

			next = time.Now().Add(200 * time.Millisecond)
		}

		conn.SetReadDeadline(next)

		size, remote, err := conn.ReadFromUDP(buf)

		if err != nil {
			continue
		}

		origin, inner, ok := session.open(buf[:size])

		if !ok || len(inner) == 0 {
			continue
		}

		clients[socketAddress(remote.IP, remote.Port)] = origin

		destination := &net.UDPAddr{IP: remote.IP, Port: targetPort}

		destinations[destination.String()] = destination

		_, known := clients[socketAddress(remote.IP, remote.Port)]

		if packetKind(buf) != kindData || !known {
			continue
		}

		data, ok := decodeData(inner)

		if !ok || !validIPv4(data.payload) || data.payload[9] != 1 {
			continue
		}

		packet := append([]byte(nil), data.payload...)
		head := int(packet[0]&15) * 4

		if len(packet) < head+8 || packet[head] != 8 {
			continue
		}

		copy(packet[12:16], data.payload[16:20])
		copy(packet[16:20], data.payload[12:16])
		packet[head] = 0
		packet[10], packet[11], packet[head+2], packet[head+3] = 0, 0, 0, 0
		binary.BigEndian.PutUint16(packet[10:12], internetChecksum(packet[:head]))
		binary.BigEndian.PutUint16(packet[head+2:head+4], internetChecksum(packet[head:]))

		destination = &net.UDPAddr{IP: remote.IP, Port: targetPort}

		send(destination, kindData, encodeData(&Data{hops: []uint16{peer.index}, payload: packet}))
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
