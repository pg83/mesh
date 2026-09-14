package main

import (
	"embed"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"time"
)

//go:embed web/*
var webFiles embed.FS

type TopologyVertex struct {
	ID string `json:"id"`
	Vertex
	Owner uint16 `json:"owner"`
}

type TopologyEdge struct {
	From string `json:"source"`
	To   string `json:"target"`
}

type Topology struct {
	Index    uint16                    `json:"index"`
	Time     string                    `json:"time"`
	Peers    []PeerConfig              `json:"peers"`
	Vertices []TopologyVertex          `json:"vertices"`
	Edges    []TopologyEdge            `json:"edges"`
	Routes   map[string][]TopologyEdge `json:"routes"`
}

func topologyEdge(edge Edge) TopologyEdge {
	return TopologyEdge{From: strconv.FormatUint(edge.From, 10), To: strconv.FormatUint(edge.To, 10)}
}

func topology(status *Status, peers []PeerConfig) *Topology {
	result := &Topology{Index: status.Index, Time: time.Now().UTC().Format(time.RFC3339), Peers: peers,
		Vertices: []TopologyVertex{}, Edges: []TopologyEdge{}, Routes: map[string][]TopologyEdge{}}

	owners := map[uint64]uint16{}

	for _, peer := range peers {
		owners[udpVertex(net.ParseIP(peer.Intip), 0).hash()] = peer.Index
	}

	for _, edge := range status.Graph {
		if index := owners[edge.From]; index != 0 && status.Addresses[edge.From].isHost() {
			owners[edge.To] = index
		}

		if index := owners[edge.To]; index != 0 && status.Addresses[edge.To].isHost() {
			owners[edge.From] = index
		}

		result.Edges = append(result.Edges, topologyEdge(edge))
	}

	for _, id := range status.Vertices {
		result.Vertices = append(result.Vertices, TopologyVertex{ID: strconv.FormatUint(id, 10), Vertex: status.Addresses[id], Owner: owners[id]})
	}

	for destination, path := range status.Routes {
		for _, edge := range path {
			result.Routes[destination] = append(result.Routes[destination], topologyEdge(edge))
		}
	}

	return result
}

func runWeb(address, control string) {
	base := "http://" + controlAddress(control)
	client := controlClient()
	mux := http.NewServeMux()

	for _, path := range []string{"topology", "config"} {
		mux.HandleFunc("GET /api/"+path, httpBoundary(func(w http.ResponseWriter, r *http.Request) {
			request := throw2(http.NewRequestWithContext(r.Context(), "GET", base+"/"+path+"?"+r.URL.RawQuery, nil))
			response := throw2(client.Do(request))

			defer response.Body.Close()

			for _, header := range []string{"Content-Type", "Content-Disposition"} {
				w.Header().Set(header, response.Header.Get(header))
			}

			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(response.StatusCode)
			throw2(io.Copy(w, response.Body))
		}))
	}

	files := http.FileServerFS(throw2(fs.Sub(webFiles, "web")))

	mux.HandleFunc("GET /config", func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = "/"
		files.ServeHTTP(w, r)
	})

	mux.Handle("GET /", files)
	serveHTTP(throw2(net.Listen("tcp", address)), mux)
}
