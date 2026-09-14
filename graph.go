package main

import (
	"bytes"
	"cmp"
	"slices"
	"time"
)

type RecordVertex struct {
	Vertex
	Ingress bool `json:"ingress"`
	Egress  bool `json:"egress"`
}

type Observation struct {
	From uint64 `json:"from"`
	Seen Vertex `json:"seen"`
}

type SeenKey struct {
	owner    uint16
	listener uint64
}

type GraphRecord struct {
	Owner    uint16         `json:"owner"`
	Version  uint64         `json:"version"`
	Vertices []RecordVertex `json:"vertices"`
	Links    []Edge         `json:"links"`
	Observed []Observation  `json:"observed"`
	packet   []byte
	applied  time.Time
}

func (n *Node) publishRecord() {
	ingress := map[uint64]bool{}
	egress := map[uint64]bool{}

	for id, local := range n.local {
		if local.receive {
			ingress[id] = true
		}
	}

	for edge, channel := range n.channelStatus {
		if channel.Outgoing {
			if n.local[edge.From] != nil {
				egress[edge.From] = true
			}
		} else if n.local[edge.To] != nil {
			ingress[edge.To] = true
		}
	}

	record := &GraphRecord{Owner: n.cfg.Index, Vertices: []RecordVertex{}, Links: []Edge{}, Observed: []Observation{}}
	exported := map[uint64]bool{}
	observed := map[uint64]SocketAddress{}

	for id := range n.local {
		vertex, known := n.addresses[id]

		if !known {
			n.log.Error("local vertex without address", "id", id, "address", n.local[id].address.string())

			continue
		}

		if ingress[id] || egress[id] {
			record.Vertices = append(record.Vertices, RecordVertex{Vertex: vertex, Ingress: ingress[id], Egress: egress[id]})
			exported[id] = true
		}
	}

	for edge := range n.observed {
		if !exported[edge.To] || edge.From == 0 || edge.From == edge.To {
			continue
		}

		record.Links = append(record.Links, edge)

		source, wire := n.addresses[edge.From], n.channelStatus[edge].Wire

		if wire.Port == 0 || !source.isTagged() || socketAddress(source.ip(), int(source.Port)) == wire {
			continue
		}

		if current, exists := observed[edge.From]; !exists || compareSocketAddress(wire, current) < 0 {
			observed[edge.From] = wire
		}
	}

	for from, wire := range observed {
		record.Observed = append(record.Observed, Observation{From: from, Seen: wire.vertex()})
	}

	slices.SortFunc(record.Vertices, func(a, b RecordVertex) int { return compareVertex(a.Vertex, b.Vertex) })
	slices.SortFunc(record.Links, compareEdge)
	slices.SortFunc(record.Observed, func(a, b Observation) int { return cmp.Compare(a.From, b.From) })

	body := encodeRecordBody(record)
	previous := n.records[n.cfg.Index]

	if previous != nil && bytes.Equal(previous.packet[recordHeader:], body) {
		return
	}

	record.Version = n.nextPacketID()
	record.packet = encodeRecord(n.cfg.Index, record.Version, body)
	record.applied = time.Now()

	n.records[n.cfg.Index] = record
}

func (n *Node) handleRecord(record *GraphRecord) {
	if previous := n.records[record.Owner]; previous != nil && record.Version <= previous.Version {
		n.metrics.recordsStale.Add(1)

		return
	}

	n.metrics.recordsApplied.Add(1)
	record.applied = time.Now()
	n.records[record.Owner] = record
}

func (n *Node) rebuild() {
	addresses := map[uint64]Vertex{}
	owners := map[uint64]uint16{}
	graph := map[Edge]bool{}

	keep := func(v Vertex) uint64 {
		v = v.canonical()

		id := v.hash()

		if id != 0 {
			addresses[id] = v
		}

		return id
	}

	for index, peer := range n.reg.byIndex {
		owners[keep(peer.vertex())] = index

		for _, ep := range peer.addresses {
			keep(ep.vertex())
		}
	}

	for index, record := range n.records {
		host := n.reg.byIndex[index].vertex().hash()

		for _, v := range record.Vertices {
			id := keep(v.Vertex)

			if id == 0 || id == host {
				continue
			}

			owners[id] = index

			if v.Ingress {
				graph[Edge{From: id, To: host}] = true
			}

			if v.Egress {
				graph[Edge{From: host, To: id}] = true
			}
		}
	}

	seen := map[SeenKey][]SocketAddress{}

	for _, record := range n.records {
		for _, link := range record.Links {
			if owners[link.From] != 0 && owners[link.To] != 0 && link.From != link.To {
				graph[link] = true
			}
		}

		for _, o := range record.Observed {
			source, owner := addresses[o.From], owners[o.From]

			if owner == 0 || !source.isTagged() {
				continue
			}

			key := SeenKey{owner: owner, listener: source.plain().hash()}

			seen[key] = append(seen[key], socketAddress(o.Seen.ip(), int(o.Seen.Port)))
		}
	}

	for key, wires := range seen {
		slices.SortFunc(wires, compareSocketAddress)
		seen[key] = slices.Compact(wires)
	}

	for id := range n.local {
		keep(n.addresses[id])
	}

	for _, actor := range n.channels {
		keep(actor.io.source)
		keep(actor.io.target)
	}

	for edge := range n.observed {
		if v, ok := n.addresses[edge.From]; ok {
			keep(v)
		}
	}

	n.addresses = addresses
	n.owners = owners
	n.seen = seen
	n.graph = graph
	n.recompute()
}

func (n *Node) recompute() {
	adjacency := map[uint64][]uint64{}

	for edge := range n.graph {
		adjacency[edge.From] = append(adjacency[edge.From], edge.To)
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

			if n.owners[from] != n.owners[cur] {
				hops++
			}
		}

		if hops == 0 || hops > maxHops {
			continue
		}

		slices.Reverse(path)
		routes[dst] = path
	}

	n.routes = routes
	n.hops = map[uint64][]uint16{}

	for dst, path := range routes {
		nodes := []uint16{}

		for _, edge := range path {
			if n.addresses[edge.To].isHost() {
				nodes = append(nodes, n.owners[edge.To])
			}
		}

		if len(nodes) != 0 && slices.Max(nodes) < 256 {
			n.hops[dst] = nodes
		}
	}

	n.next = map[uint16]Edge{}

	for edge, status := range n.channelStatus {
		if !status.Outgoing || !n.graph[edge] {
			continue
		}

		peer := n.owners[edge.To]
		current, exists := n.next[peer]

		if !exists || compareVertex(n.addresses[edge.From], n.addresses[current.From]) < 0 {
			n.next[peer] = edge
		}
	}
}
