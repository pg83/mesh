package main

import (
	"encoding/binary"
	"slices"
	"time"
)

const (
	multicastDelete    = 1
	multicastHeader    = 12
	multicastBatchSize = (1000 - multicastHeader - 3) / 8
)

type DeleteVertices []uint64

type MulticastID struct {
	origin uint16
	id     uint64
}

type Multicast struct {
	key  MulticastID
	hops byte
	body []byte
}

func encodeMulticast(m Multicast) []byte {
	out := binary.LittleEndian.AppendUint16([]byte{innerMulticast}, m.key.origin)

	out = binary.LittleEndian.AppendUint64(out, m.key.id)
	out = append(out, m.hops)

	return append(out, m.body...)
}

func decodeMulticast(inner []byte) (Multicast, bool) {
	if len(inner) <= multicastHeader || len(inner) > 1000 {
		return Multicast{}, false
	}

	m := Multicast{key: MulticastID{origin: binary.LittleEndian.Uint16(inner[1:]), id: binary.LittleEndian.Uint64(inner[3:])}, hops: inner[11], body: inner[multicastHeader:]}

	return m, m.key.origin != 0 && m.key.id != 0 && m.hops > 0 && m.hops <= maxHops
}

func encodeDelete(vertices DeleteVertices) []byte {
	out := binary.LittleEndian.AppendUint16([]byte{multicastDelete}, uint16(len(vertices)))

	for _, id := range vertices {
		out = binary.LittleEndian.AppendUint64(out, id)
	}

	return out
}

func decodeDelete(body []byte) (DeleteVertices, bool) {
	if len(body) < 3 || body[0] != multicastDelete {
		return nil, false
	}

	count := int(binary.LittleEndian.Uint16(body[1:]))

	if count == 0 || count > multicastBatchSize || len(body) != 3+8*count {
		return nil, false
	}

	vertices := make(DeleteVertices, count)

	for i := range vertices {
		vertices[i] = binary.LittleEndian.Uint64(body[3+8*i:])

		if vertices[i] == 0 {
			return nil, false
		}
	}

	return vertices, true
}

func (n *Node) multicastLoop() {
	var view *Snapshot

	pending := map[uint64]bool{}
	seen := map[MulticastID]time.Time{}
	id := uint64(time.Now().UnixNano())
	timer := time.NewTimer(time.Hour)

	timer.Stop()

	defer timer.Stop()

	ticker := time.NewTicker(time.Second)

	defer ticker.Stop()

	send := func(m Multicast) {
		if view == nil {
			return
		}

		inner := encodeMulticast(m)

		for _, channel := range view.channels {
			if channel.outgoing {
				channel.post(Outbound{inner: inner})
			}
		}
	}

	for {
		select {
		case message := <-n.multicast.out:
			switch v := message.(type) {
			case *Snapshot:
				view = v
			case DeleteVertices:
				if len(pending) == 0 && len(v) != 0 {
					timer.Reset(100 * time.Millisecond)
				}

				for _, vertex := range v {
					pending[vertex] = true
				}
			case Multicast:
				if view == nil || view.registry.byIndex[v.key.origin] == nil {
					continue
				}

				if _, exists := seen[v.key]; exists {
					continue
				}

				seen[v.key] = time.Now()

				if vertices, ok := decodeDelete(v.body); ok {
					post(n.events.in, any(vertices))
				}

				if v.hops > 1 {
					v.hops--
					send(v)
				}
			}
		case <-timer.C:
			vertices := make(DeleteVertices, 0, len(pending))

			for vertex := range pending {
				vertices = append(vertices, vertex)
			}

			clear(pending)
			slices.Sort(vertices)

			for start := 0; start < len(vertices); start += multicastBatchSize {
				id++

				m := Multicast{key: MulticastID{origin: n.cfg.Index, id: id}, hops: maxHops, body: encodeDelete(vertices[start:min(start+multicastBatchSize, len(vertices))])}

				seen[m.key] = time.Now()
				send(m)
			}
		case now := <-ticker.C:
			for key, received := range seen {
				if now.Sub(received) >= time.Minute {
					delete(seen, key)
				}
			}
		}
	}
}

func (n *Node) forgetVertices(vertices DeleteVertices) {
	removed := map[uint64]bool{}

	for _, id := range vertices {
		if n.local[id] != nil || n.addresses[id].isHost() {
			continue
		}

		removed[id] = true
		delete(n.addresses, id)
	}

	for edge := range n.graph {
		if removed[edge.From] || removed[edge.To] {
			delete(n.graph, edge)
			delete(n.owned, edge)
			delete(n.observed, edge)
		}
	}

	n.recompute()
}
