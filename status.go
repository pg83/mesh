package main

import (
	"encoding/json"
	"net"
	"os"
	"slices"
	"strconv"
	"time"
)

type LinkStatus struct {
	Peer     uint16 `json:"peer"`
	Endpoint string `json:"endpoint"`
	Age      int    `json:"age"`
	Idle     int    `json:"idle"`
}

type Status struct {
	Index   uint16              `json:"index"`
	Links   []LinkStatus        `json:"links"`
	Nodes   []uint16            `json:"nodes"`
	Routes  map[string][]uint16 `json:"routes"`
	Pending int                 `json:"pending"`
}

func (n *Node) status() *Status {
	now := time.Now()

	st := &Status{
		Index:  n.cfg.Index,
		Links:  []LinkStatus{},
		Nodes:  []uint16{},
		Routes: map[string][]uint16{},
	}

	n.mu.Lock()

	defer n.mu.Unlock()

	for _, s := range n.sessions {
		st.Links = append(st.Links, LinkStatus{
			Peer:     s.peer,
			Endpoint: s.endpoint.String(),
			Age:      int(now.Sub(s.created).Seconds()),
			Idle:     int(now.Sub(s.lastRecv).Seconds()),
		})
	}

	for index := range n.ads {
		st.Nodes = append(st.Nodes, index)
	}

	slices.Sort(st.Nodes)

	for dst, path := range n.routes {
		st.Routes[strconv.Itoa(int(dst))] = path
	}

	st.Pending = len(n.pending)

	return st
}

func (n *Node) statusLoop() {
	if n.cfg.Status == "" {
		return
	}

	os.Remove(n.cfg.Status)

	ln := throw2(net.Listen("unix", n.cfg.Status))

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

	buf := make([]byte, 1<<20)
	size := throw2(conn.Read(buf))

	os.Stdout.Write(buf[:size])
}
