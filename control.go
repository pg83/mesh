package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

func controlAddress(address string) string {
	host, port := throw3(net.SplitHostPort(address))

	if host == "localhost" {
		host = "127.0.0.1"
	}

	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		throwFmt("control address must be a loopback IP or localhost")
	}

	n := throw2(strconv.Atoi(port))

	if n < 1 || n > 65535 {
		throwFmt("bad control port")
	}

	return net.JoinHostPort(host, port)
}

func httpBoundary(body func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		try(func() { body(w, r) }).catch(func(e *Exception) {
			http.Error(w, e.Error(), http.StatusBadGateway)
		})
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	data := throw2(json.MarshalIndent(value, "", "  "))

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	throw2(w.Write(append(data, '\n')))
}

func (n *Node) readStatus(ctx context.Context) *Status {
	reply := make(chan *Status, 1)

	select {
	case n.events.in <- reply:
	case <-ctx.Done():
		throw(ctx.Err())
	}

	select {
	case status := <-reply:
		return status
	case <-ctx.Done():
		throw(ctx.Err())
	}

	return nil
}

func (n *Node) publicRegistry(ctx context.Context) []PeerConfig {
	peers := []PeerConfig{}

	for _, record := range n.currentSnapshot(ctx).registry.records() {
		peers = append(peers, record.PeerConfig)
	}

	return peers
}

func (n *Node) exportConfig(w http.ResponseWriter, r *http.Request) {
	peers := n.publicRegistry(r.Context())
	name := r.URL.Query().Get("node")

	for _, peer := range peers {
		if name == "" || (name != peer.Name && name != strconv.Itoa(int(peer.Index))) {
			continue
		}

		if len(peer.Endpoint) != 0 {
			http.Error(w, "node has static endpoints", http.StatusBadRequest)

			return
		}

		bootstrap := []PeerConfig{}

		for _, candidate := range peers {
			if candidate.Index == peer.Index || len(candidate.Endpoint) != 0 {
				bootstrap = append(bootstrap, candidate)
			}
		}

		cfg := Config{
			Index: peer.Index, Subnet: n.cfg.Subnet, Mtu: n.cfg.Mtu,
			Control: "127.0.0.1:8058", Registry: bootstrap,
			Endpoint: []EndpointConfig{},
		}

		w.Header().Set("Content-Disposition", "attachment; filename=mesh-"+strconv.Itoa(int(peer.Index))+".json")
		writeJSON(w, cfg)

		return
	}

	http.Error(w, "unknown node; specify ?node=name or ?node=index", http.StatusNotFound)
}

func (n *Node) controlLoop() {
	if n.cfg.Control == "" {
		return
	}

	address := controlAddress(n.cfg.Control)
	mux := http.NewServeMux()

	mux.HandleFunc("GET /status", httpBoundary(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, n.readStatus(r.Context()))
	}))

	mux.HandleFunc("GET /topology", httpBoundary(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, topology(n.readStatus(r.Context()), n.publicRegistry(r.Context())))
	}))

	mux.HandleFunc("GET /config", httpBoundary(n.exportConfig))
	serveHTTP(throw2(net.Listen("tcp", address)), mux)
}

func serveHTTP(listener net.Listener, handler http.Handler) {
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: time.Minute}

	throw(server.Serve(listener))
}

func controlClient() *http.Client {
	return &http.Client{
		Timeout:       10 * time.Second,
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func showStatus(address string) {
	response := throw2(controlClient().Get("http://" + controlAddress(address) + "/status"))

	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		throwFmt("control: %s", response.Status)
	}

	throw2(io.Copy(os.Stdout, response.Body))
}
