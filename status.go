package main

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"time"
)

const sockPathMax = 108

type LinkStatus struct {
	Edge
	Idle int `json:"idle"`
}

type WSStatus struct {
	Edge
	Origin uint64 `json:"origin"`
	ID     uint64 `json:"id"`
}

type Status struct {
	Connections []WSStatus          `json:"connections"`
	Dialing     int                 `json:"dialing"`
	Endpoints   map[uint64]Endpoint `json:"endpoints"`
	Index       uint16              `json:"index"`
	Links       []LinkStatus        `json:"links"`
	Graph       []Update            `json:"graph"`
	Vertices    []uint64            `json:"vertices"`
	Routes      map[string][]Edge   `json:"routes"`
}

func (n *Node) status() *Status {
	now := time.Now()
	st := &Status{Index: n.cfg.Index, Links: []LinkStatus{}, Graph: []Update{}, Vertices: []uint64{}, Routes: map[string][]Edge{}}

	st.Endpoints = map[uint64]Endpoint{}

	for id, ep := range n.addresses {
		st.Endpoints[id] = ep
	}

	st.Connections = []WSStatus{}
	st.Dialing = len(n.dialing)

	for _, c := range n.ws {
		st.Connections = append(st.Connections, c)
	}

	for edge, received := range n.observed {
		st.Links = append(st.Links, LinkStatus{Edge: edge, Idle: int(now.Sub(received).Seconds())})
	}

	vertices := map[uint64]bool{}

	for edge, record := range n.graph {
		if !record.Alive {
			continue
		}

		st.Graph = append(st.Graph, Update{Edge: edge, State: record})

		vertices[edge.From] = true
		vertices[edge.To] = true
	}

	for ep := range vertices {
		st.Vertices = append(st.Vertices, ep)
	}

	slices.Sort(st.Vertices)
	slices.SortFunc(st.Graph, func(a, b Update) int { return compareEdge(a.Edge, b.Edge) })
	slices.SortFunc(st.Links, func(a, b LinkStatus) int { return compareEdge(a.Edge, b.Edge) })

	for dst, path := range n.routes {
		st.Routes[n.addresses[dst].string()] = path
	}

	return st
}

func (n *Node) statusLoop() {
	path := n.cfg.Status

	if path == "" {
		return
	}

	if !strings.HasPrefix(path, "@") {
		if len(path) >= sockPathMax {
			throwFmt("status socket path is %d bytes, the limit is %d", len(path), sockPathMax-1)
		}

		os.Remove(path)
	}

	ln := throw2(net.Listen("unix", path))

	for {
		conn := throw2(ln.Accept())
		reply := make(chan *Status, 1)

		n.events.in <- reply

		out := throw2(json.Marshal(<-reply))

		conn.Write(append(out, '\n'))
		conn.Close()
	}
}

func showStatus(path string) {
	conn := throw2(net.Dial("unix", path))

	defer conn.Close()

	throw2(io.Copy(os.Stdout, conn))
}
