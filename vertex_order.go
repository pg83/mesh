package main

import (
	"bytes"
	"cmp"
	"net/netip"
	"slices"
)

type VertexKey struct {
	proto string
	addr  string
	four  [4]byte
	is4   bool
	port  uint16
	path  string
	id    uint32
}

func vertexKey(id uint32, v Vertex) VertexKey {
	key := VertexKey{proto: v.Proto, addr: v.Addr, port: v.Port, path: v.Path, id: id}

	if addr, err := netip.ParseAddr(v.Addr); err == nil {
		if unmapped := addr.Unmap(); unmapped.Is4() {
			key.four, key.is4 = unmapped.As4(), true
		}
	}

	return key
}

func compareVertexKey(a, b VertexKey) int {
	if v := cmp.Compare(a.proto, b.proto); v != 0 {
		return v
	}

	if a.is4 && b.is4 {
		if v := bytes.Compare(a.four[:], b.four[:]); v != 0 {
			return v
		}
	} else if v := cmp.Compare(a.addr, b.addr); v != 0 {
		return v
	}

	if v := cmp.Compare(a.port, b.port); v != 0 {
		return v
	}

	if v := cmp.Compare(a.path, b.path); v != 0 {
		return v
	}

	return cmp.Compare(a.id, b.id)
}

type VertexOrder struct {
	ids  []uint32
	rank map[uint32]int32
}

func newVertexOrder(addresses map[uint32]Vertex, graph map[Edge]bool) *VertexOrder {
	keys := make([]VertexKey, 0, len(addresses))
	placed := make(map[uint32]bool, len(addresses))

	add := func(id uint32) {
		if !placed[id] {
			placed[id] = true
			keys = append(keys, vertexKey(id, addresses[id]))
		}
	}

	for id := range addresses {
		add(id)
	}

	for edge := range graph {
		add(edge.From)
		add(edge.To)
	}

	slices.SortFunc(keys, compareVertexKey)

	o := &VertexOrder{ids: make([]uint32, len(keys)), rank: make(map[uint32]int32, len(keys))}

	for i, key := range keys {
		o.ids[i] = key.id
		o.rank[key.id] = int32(i)
	}

	return o
}

func (o *VertexOrder) index(id uint32) (int32, bool) {
	rank, known := o.rank[id]

	return rank, known
}

func (o *VertexOrder) compare(a, b uint32) int {
	first, known := o.rank[a]
	second, present := o.rank[b]

	if !known || !present {
		return cmp.Compare(a, b)
	}

	return cmp.Compare(first, second)
}
