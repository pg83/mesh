package main

func (n *Node) readPacket(packet []byte, view *Snapshot) (*Session, Vertex, []byte, bool) {
	if len(packet) < headerTransport || !validPacketType(packetKind(packet)) {
		return nil, Vertex{}, nil, false
	}

	peer := packetSender(packet)

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
