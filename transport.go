package main

func (n *Node) readPacket(packet []byte, view *Snapshot) (*Session, uint32, []byte, bool) {
	if len(packet) < headerTransport {
		return nil, 0, nil, false
	}

	peer := packetSender(packet)

	if peer == n.cfg.Index || view.registry.byIndex[peer] == nil {
		return nil, 0, nil, false
	}

	session := view.registry.byIndex[peer].session
	source, inner, ok := session.open(packet)

	if !ok {
		return nil, source, nil, false
	}

	return session, source, inner, true
}
