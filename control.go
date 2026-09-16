package main

import (
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
