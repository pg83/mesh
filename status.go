package main

import (
	"encoding/json"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

const sockPathMax = 108

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

// statusLoop serves one JSON document per connection. A name starting with
// "@" is an abstract socket: it lives in the network namespace, has no path,
// and so escapes the 108 byte limit on socket paths.
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

	buf := make([]byte, 1<<20)
	size := throw2(conn.Read(buf))

	os.Stdout.Write(buf[:size])
}
