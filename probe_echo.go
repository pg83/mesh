//go:build meshprobe

package main

import (
	"encoding/binary"
	"net"
	"time"
)

func echoProbe(path, remote string, index uint16) {
	cfg := loadConfig(path)
	reg := newRegistry(cfg.Registry, cfg.RegistryVersion)
	me, peer := reg.byIndex[cfg.Index], reg.byIndex[index]
	key := deriveKey(decodeKey(cfg.Key))
	session := newSession(me, peer, key.private)
	conn := throw2(net.ListenUDP(cfg.Endpoint[0].description().socketKey().network("udp"), cfg.Endpoint[0].binding()))
	target := parseUDPAddr(remote)
	mine, other := cfg.Endpoint[0].description(), endpoint(target.IP, target.Port)
	buf := make([]byte, maxPacket)
	received := false
	next := time.Time{}
	id := uint64(time.Now().UnixNano())

	send := func(inner []byte) {
		id++
		throw2(conn.WriteToUDP(session.seal(inner, id), target))
	}

	for {
		if time.Now().After(next) {
			edges := []Edge{{From: me.endpoint().hash(), To: mine.hash()}, {From: mine.hash(), To: me.endpoint().hash()}}

			if received {
				edges = append(edges, Edge{From: other.hash(), To: mine.hash()})
			}

			ad := &Ad{Endpoints: []Endpoint{me.endpoint(), mine, other}}

			for _, edge := range edges {
				ad.Edges = append(ad.Edges, Update{Edge: edge, State: State{ID: uint64(time.Now().UnixNano()), Alive: true}})
			}

			send(encodeAd(ad))
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

		target = remote
		other = endpoint(remote.IP, remote.Port)
		received = true

		if inner[0] != innerData {
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
		send(encodeData(&Data{path: []Edge{{From: mine.hash(), To: other.hash()}}, ip: packet}))
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
