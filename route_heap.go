package main

import (
	"cmp"
	"slices"
)

type RouteEntry struct {
	cost int32
	hops int32
	path []int32
	id   int32
}

func compareRouteEntry(a, b RouteEntry) int {
	if v := cmp.Compare(a.cost, b.cost); v != 0 {
		return v
	}

	if v := cmp.Compare(a.hops, b.hops); v != 0 {
		return v
	}

	return slices.Compare(a.path, b.path)
}

type RouteHeap struct {
	items []RouteEntry
}

func (h *RouteHeap) empty() bool {
	return len(h.items) == 0
}

func (h *RouteHeap) push(entry RouteEntry) {
	h.items = append(h.items, entry)

	for child := len(h.items) - 1; child > 0; {
		parent := (child - 1) / 2

		if compareRouteEntry(h.items[child], h.items[parent]) >= 0 {
			break
		}

		h.items[child], h.items[parent] = h.items[parent], h.items[child]
		child = parent
	}
}

func (h *RouteHeap) pop() RouteEntry {
	top := h.items[0]
	last := len(h.items) - 1

	h.items[0] = h.items[last]
	h.items[last] = RouteEntry{}
	h.items = h.items[:last]

	for parent := 0; ; {
		least := parent

		for _, child := range [2]int{2*parent + 1, 2*parent + 2} {
			if child < len(h.items) && compareRouteEntry(h.items[child], h.items[least]) < 0 {
				least = child
			}
		}

		if least == parent {
			return top
		}

		h.items[parent], h.items[least] = h.items[least], h.items[parent]
		parent = least
	}
}
