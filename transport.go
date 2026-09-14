package main

import "encoding/binary"

func (n *Node) readPacket(packet []byte, view *Snapshot) (*Session, Vertex, []byte, bool) {
	if len(packet) < headerTransport || !validPacketType(packet[0]) {
		return nil, Vertex{}, nil, false
	}

	peer := binary.LittleEndian.Uint16(packet[1:])

	if peer == n.cfg.Index || view.registry.byIndex[peer] == nil {
		return nil, Vertex{}, nil, false
	}

	session := view.registry.byIndex[peer].session
	source, inner, ok := session.open(packet)

	if !ok {
		return nil, source, nil, false
	}

	if owner := view.owners[source.hash()]; owner != 0 && owner != peer {
		return nil, source, nil, false
	}

	return session, source, inner, true
}
