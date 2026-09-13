package main

import "slices"

type State struct {
	ID    uint64 `json:"id"`
	Alive bool   `json:"alive"`
}

type Update struct {
	Edge
	State
}

func (n *Node) record(edge Edge, alive bool) {
	n.graph[edge] = State{ID: n.nextPacketID(), Alive: alive}
}

func (n *Node) recompute() {
	adjacency := map[uint64][]uint64{}
	owners := map[uint64]uint16{}

	for index, peer := range n.reg.byIndex {
		owners[peer.vertex().hash()] = index
	}

	for edge, record := range n.graph {
		if !record.Alive {
			continue
		}

		adjacency[edge.From] = append(adjacency[edge.From], edge.To)

		if index := owners[edge.From]; n.addresses[edge.From].isHost() && index != 0 && !n.addresses[edge.To].isHost() {
			owners[edge.To] = index
		}

		if index := owners[edge.To]; n.addresses[edge.To].isHost() && index != 0 && !n.addresses[edge.From].isHost() {
			owners[edge.From] = index
		}
	}

	for _, neighbors := range adjacency {
		slices.SortFunc(neighbors, func(a, b uint64) int { return compareVertex(n.addresses[a], n.addresses[b]) })
	}

	source := n.reg.byIndex[n.cfg.Index].vertex().hash()
	prev := map[uint64]uint64{}
	seen := map[uint64]bool{source: true}
	queue := []uint64{source}

	for len(queue) > 0 {
		cur := queue[0]

		queue = queue[1:]

		for _, next := range adjacency[cur] {
			if seen[next] {
				continue
			}

			seen[next] = true
			prev[next] = cur
			queue = append(queue, next)
		}
	}

	routes := map[uint64][]Edge{}

	for dst := range seen {
		path := []Edge{}
		hops := 0

		for cur := dst; cur != source; cur = prev[cur] {
			from := prev[cur]

			path = append(path, Edge{From: from, To: cur})

			if owners[from] != owners[cur] {
				hops++
			}
		}

		if hops == 0 || hops > maxHops || len(path) > maxRouteEdges {
			continue
		}

		slices.Reverse(path)
		routes[dst] = path
	}

	n.owners = owners
	n.routes = routes
}
