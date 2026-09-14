package main

import (
	"bytes"
	"cmp"
	"slices"
	"time"
)

type RecordVertex struct {
	ID uint32 `json:"id"`
	Vertex
	Ingress bool `json:"ingress"`
	Egress  bool `json:"egress"`
}

type Observation struct {
	From uint32 `json:"from"`
	Seen Vertex `json:"seen"`
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
	ingress := map[uint32]bool{}
	egress := map[uint32]bool{}

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
	exported := map[uint32]bool{}
	observed := map[uint32]SocketAddress{}

	for id := range n.local {
		vertex, known := n.addresses[id]

		if !known {
			n.log.Error("local vertex without address", "id", id, "address", n.local[id].address.string())

			continue
		}

		if ingress[id] || egress[id] {
			record.Vertices = append(record.Vertices, RecordVertex{ID: id, Vertex: vertex, Ingress: ingress[id], Egress: egress[id]})
			exported[id] = true
		}
	}

	for edge := range n.observed {
		if !exported[edge.To] || edge.From == 0 || edge.From == edge.To {
			continue
		}

		record.Links = append(record.Links, edge)

		source, known := n.addresses[edge.From]
		wire := n.channelStatus[edge].Wire

		if wire.Port == 0 || !known || source.Proto != "udp" || socketAddress(source.ip(), int(source.Port)) == wire {
			continue
		}

		if current, exists := observed[edge.From]; !exists || compareSocketAddress(wire, current) < 0 {
			observed[edge.From] = wire
		}
	}

	for from, wire := range observed {
		record.Observed = append(record.Observed, Observation{From: from, Seen: wire.vertex()})
	}

	slices.SortFunc(record.Vertices, func(a, b RecordVertex) int { return cmp.Compare(a.ID, b.ID) })
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
	addresses := map[uint32]Vertex{}
	graph := map[Edge]bool{}
	present := map[uint32]bool{}

	for index, peer := range n.reg.byIndex {
		addresses[hostID(index)] = peer.vertex()

		for i, ep := range peer.addresses {
			addresses[peer.endpointID(i)] = ep.vertex()
		}
	}

	for index, record := range n.records {
		host := hostID(index)

		for _, v := range record.Vertices {
			addresses[v.ID] = v.Vertex
			present[v.ID] = true

			if v.Ingress {
				graph[Edge{From: v.ID, To: host}] = true
			}

			if v.Egress {
				graph[Edge{From: host, To: v.ID}] = true
			}
		}
	}

	listener := func(id uint32) uint32 {
		source := addresses[id]

		for _, v := range n.records[vertexOwner(id)].Vertices {
			if v.Ingress && v.isEndpoint() && v.Proto == "udp" && v.Addr == source.Addr && v.Port == source.Port {
				return v.ID
			}
		}

		return 0
	}

	seen := map[uint32][]SocketAddress{}

	for _, record := range n.records {
		for _, link := range record.Links {
			if present[link.From] && link.From != link.To {
				graph[link] = true
			}
		}

		for _, o := range record.Observed {
			if !present[o.From] {
				continue
			}

			if id := listener(o.From); id != 0 {
				seen[id] = append(seen[id], socketAddress(o.Seen.ip(), int(o.Seen.Port)))
			}
		}
	}

	for id, wires := range seen {
		slices.SortFunc(wires, compareSocketAddress)
		seen[id] = slices.Compact(wires)
	}

	for id := range n.local {
		if v, ok := n.addresses[id]; ok {
			addresses[id] = v
		}
	}

	for _, actor := range n.channels {
		if actor.io.source.valid() {
			addresses[actor.io.edge.From] = actor.io.source
		}

		if actor.io.target.valid() {
			addresses[actor.io.edge.To] = actor.io.target
		}
	}

	n.addresses = addresses
	n.seen = seen
	n.graph = graph
	n.recompute()
}

func (n *Node) compareIDs(a, b uint32) int {
	if v := compareVertex(n.addresses[a], n.addresses[b]); v != 0 {
		return v
	}

	return cmp.Compare(a, b)
}

func (n *Node) recompute() {
	adjacency := map[uint32][]uint32{}

	for edge := range n.graph {
		adjacency[edge.From] = append(adjacency[edge.From], edge.To)
	}

	for _, neighbors := range adjacency {
		slices.SortFunc(neighbors, n.compareIDs)
	}

	source := hostID(n.cfg.Index)
	prev := map[uint32]uint32{}
	seen := map[uint32]bool{source: true}
	queue := []uint32{source}

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

	routes := map[uint32][]Edge{}

	for dst := range seen {
		path := []Edge{}
		hops := 0

		for cur := dst; cur != source; cur = prev[cur] {
			from := prev[cur]

			path = append(path, Edge{From: from, To: cur})

			if vertexOwner(from) != vertexOwner(cur) {
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
	n.hops = map[uint32][]uint16{}

	for dst, path := range routes {
		nodes := []uint16{}

		for _, edge := range path {
			if isHostID(edge.To) {
				nodes = append(nodes, vertexOwner(edge.To))
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

		peer := vertexOwner(edge.To)
		current, exists := n.next[peer]

		if !exists || n.compareIDs(edge.From, current.From) < 0 {
			n.next[peer] = edge
		}
	}
}
