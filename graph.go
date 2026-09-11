package main

import "slices"

type Update struct {
	Edge
	ID    uint64 `json:"id"`
	Alive bool   `json:"alive"`
}

func (n *Node) record(edge Edge, alive bool) {
	n.graph[edge] = &Update{Edge: edge, ID: n.nextPacketID(), Alive: alive}
}

func (n *Node) recompute() {
	adjacency := map[Endpoint][]Endpoint{}
	owners := map[Endpoint]uint16{}

	for index, peer := range n.reg.byIndex {
		owners[peer.endpoint()] = index
	}

	for edge, record := range n.graph {
		if !record.Alive {
			continue
		}

		adjacency[edge.From] = append(adjacency[edge.From], edge.To)

		if index := owners[edge.From]; edge.From.Port == 0 && index != 0 && edge.To.Port != 0 {
			owners[edge.To] = index
		}

		if index := owners[edge.To]; edge.To.Port == 0 && index != 0 && edge.From.Port != 0 {
			owners[edge.From] = index
		}
	}

	for _, neighbors := range adjacency {
		slices.SortFunc(neighbors, compareEndpoint)
	}

	source := n.reg.byIndex[n.cfg.Index].endpoint()
	prev := map[Endpoint]Endpoint{}
	seen := map[Endpoint]bool{source: true}
	queue := []Endpoint{source}

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

	routes := map[Endpoint][]Edge{}

	for dst := range seen {
		path := []Edge{}

		for cur := dst; cur != source; cur = prev[cur] {
			from := prev[cur]

			if owners[from] != owners[cur] {
				path = append(path, Edge{From: from, To: cur})
			}
		}

		if len(path) == 0 || len(path) > maxHops {
			continue
		}

		slices.Reverse(path)
		routes[dst] = path
	}

	n.owners = owners
	n.routes = routes
}
