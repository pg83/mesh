package main

import "slices"

func (n *Node) edges(index uint16) []uint16 {
	if index == n.cfg.Index {
		return n.neighbors()
	}

	if known := n.ads[index]; known != nil {
		return known.ad.Neighbors
	}

	return nil
}

func (n *Node) recompute() {
	prev := map[uint16]uint16{}
	seen := map[uint16]bool{n.cfg.Index: true}
	queue := []uint16{n.cfg.Index}

	for len(queue) > 0 {
		cur := queue[0]

		queue = queue[1:]

		for _, next := range n.edges(cur) {
			if seen[next] {
				continue
			}

			seen[next] = true
			prev[next] = cur
			queue = append(queue, next)
		}
	}

	routes := make(map[uint16][]uint16, len(seen))

	for dst := range seen {
		if path := walkBack(prev, n.cfg.Index, dst); path != nil {
			routes[dst] = path
		}
	}

	n.routes = routes
}

func walkBack(prev map[uint16]uint16, src, dst uint16) []uint16 {
	path := []uint16{}

	for cur := dst; cur != src; cur = prev[cur] {
		path = append(path, cur)

		if len(path) > maxHops {
			return nil
		}
	}

	if len(path) == 0 {
		return nil
	}

	slices.Reverse(path)

	return path
}
