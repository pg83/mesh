package main

import (
	"encoding/binary"
	"encoding/json"
)

type Binding struct {
	From  Vertex `json:"from"`
	To    Vertex `json:"to"`
	Reply bool   `json:"reply,omitempty"`
}

func (n *Node) readBinding(packet []byte, view *Snapshot) (*Session, Binding, bool) {
	binding := Binding{}

	if len(packet) < headerTransport {
		return nil, binding, false
	}

	peer := binary.LittleEndian.Uint16(packet[1:])

	if peer == n.cfg.Index || view.registry.byIndex[peer] == nil {
		return nil, binding, false
	}

	session := view.registry.byIndex[peer].session
	inner, ok := session.open(packet)

	if !ok || len(inner) == 0 || inner[0] != innerBinding || json.Unmarshal(inner[1:], &binding) != nil {
		return nil, binding, false
	}

	binding.From = binding.From.canonical()
	binding.To = binding.To.canonical()

	if !binding.From.valid() || !binding.To.valid() || (binding.Reply && (binding.To.Proto != "source" || binding.To.Node != n.cfg.Index || !binding.From.isEndpoint())) || (!binding.Reply && (binding.From.Proto != "source" || binding.From.Node != peer || !binding.To.isEndpoint())) {
		return nil, binding, false
	}

	if owner := view.owners[binding.From.hash()]; owner != 0 && owner != peer {
		return nil, binding, false
	}

	return session, binding, true
}

func bindingPacket(session *Session, source, target Vertex, id uint64) []byte {
	blob := throw2(json.Marshal(Binding{From: source, To: target}))

	return session.seal(append([]byte{innerBinding}, blob...), id)
}

func bindingReply(session *Session, source, target Vertex, id uint64) []byte {
	return session.seal(bindingInner(Binding{From: source, To: target, Reply: true}), id)
}

func bindingInner(binding Binding) []byte {
	return append([]byte{innerBinding}, throw2(json.Marshal(binding))...)
}
