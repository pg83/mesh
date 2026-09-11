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

type Status struct {
	Index    uint16            `json:"index"`
	Links    []LinkStatus      `json:"links"`
	Graph    []Update          `json:"graph"`
	Vertices []Endpoint        `json:"vertices"`
	Routes   map[string][]Edge `json:"routes"`
}

func (n *Node) status() *Status {
	now := time.Now()
	st := &Status{Index: n.cfg.Index, Links: []LinkStatus{}, Graph: []Update{}, Vertices: []Endpoint{}, Routes: map[string][]Edge{}}

	n.mu.Lock()

	defer n.mu.Unlock()

	for edge, received := range n.observed {
		st.Links = append(st.Links, LinkStatus{Edge: edge, Idle: int(now.Sub(received).Seconds())})
	}

	vertices := map[Endpoint]bool{}

	for edge, record := range n.graph {
		if !record.alive(now) {
			continue
		}

		update := record.Update

		update.TTL = uint32(record.expires.Sub(now) / time.Millisecond)
		st.Graph = append(st.Graph, update)

		vertices[edge.From] = true
		vertices[edge.To] = true
	}

	for ep := range vertices {
		st.Vertices = append(st.Vertices, ep)
	}

	slices.SortFunc(st.Vertices, compareEndpoint)
	slices.SortFunc(st.Graph, func(a, b Update) int { return compareEdge(a.Edge, b.Edge) })
	slices.SortFunc(st.Links, func(a, b LinkStatus) int { return compareEdge(a.Edge, b.Edge) })

	for dst, path := range n.routes {
		st.Routes[dst.string()] = path
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
		out := throw2(json.Marshal(n.status()))

		conn.Write(append(out, '\n'))
		conn.Close()
	}
}

func showStatus(path string) {
	conn := throw2(net.Dial("unix", path))

	defer conn.Close()

	throw2(io.Copy(os.Stdout, conn))
}
