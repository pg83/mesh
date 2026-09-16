package main

import (
	"maps"
	"slices"
	"time"
)

type LinkStatus struct {
	Edge
	Idle int `json:"idle"`
}

type ChannelStatus struct {
	Transport string `json:"transport"`
	Edge
	Outgoing bool          `json:"outgoing"`
	ID       uint64        `json:"id"`
	Wire     SocketAddress `json:"wire"`
}

type Status struct {
	Registry   []RegistryRecord    `json:"registry"`
	Channels   []ChannelStatus     `json:"channels"`
	Dialing    int                 `json:"dialing"`
	Addresses  map[uint32]Vertex   `json:"addresses"`
	Index      uint16              `json:"index"`
	Subnet     string              `json:"subnet"`
	Tun        string              `json:"tun"`
	Links      []LinkStatus        `json:"links"`
	Alive      []uint16            `json:"alive"`
	Graph      []Edge              `json:"graph"`
	Records    []*GraphRecord      `json:"records"`
	Vectors    []*Vector           `json:"vectors"`
	Bundles    int                 `json:"bundles"`
	Vertices   []uint32            `json:"vertices"`
	Routes     map[string][]Edge   `json:"routes"`
	DnsRecords map[string][]string `json:"dns_records,omitempty"`
}

func (n *Node) status() *Status {
	now := time.Now()
	st := &Status{Index: n.cfg.Index, Subnet: n.cfg.Subnet, Tun: n.cfg.Tun, Links: []LinkStatus{}, Graph: []Edge{}, Records: []*GraphRecord{}, Vectors: []*Vector{}, Vertices: []uint32{}, Routes: map[string][]Edge{}}

	st.Alive = slices.Sorted(maps.Keys(n.alive))
	st.Registry = n.reg.records()
	st.DnsRecords = n.cfg.DnsRecords
	st.Addresses = map[uint32]Vertex{}

	for id, ep := range n.addresses {
		st.Addresses[id] = ep
	}

	st.Channels = []ChannelStatus{}

	for _, attempt := range n.dials {
		if attempt.pending {
			st.Dialing++
		}
	}

	for _, c := range n.channelStatus {
		st.Channels = append(st.Channels, c)
	}

	for edge, received := range n.observed {
		st.Links = append(st.Links, LinkStatus{Edge: edge, Idle: int(now.Sub(received).Seconds())})
	}

	vertices := map[uint32]bool{}

	for edge := range n.graph {
		st.Graph = append(st.Graph, edge)

		vertices[edge.From] = true
		vertices[edge.To] = true
	}

	for _, owner := range slices.Sorted(maps.Keys(n.records)) {
		st.Records = append(st.Records, n.records[owner])
	}

	st.Bundles = len(n.bundles())

	for _, owner := range slices.Sorted(maps.Keys(n.vectors)) {
		st.Vectors = append(st.Vectors, n.vectors[owner])
	}

	for ep := range vertices {
		st.Vertices = append(st.Vertices, ep)
	}

	slices.SortFunc(st.Channels, func(a, b ChannelStatus) int { return compareEdge(a.Edge, b.Edge) })
	slices.Sort(st.Vertices)
	slices.SortFunc(st.Graph, compareEdge)
	slices.SortFunc(st.Links, func(a, b LinkStatus) int { return compareEdge(a.Edge, b.Edge) })

	for dst, path := range n.routes {
		if isHostID(dst) {
			st.Routes[n.describe(dst)] = path
		}
	}

	return st
}
