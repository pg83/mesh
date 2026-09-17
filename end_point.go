package main

import (
	"cmp"
	"net"
	"net/url"
	"strconv"
	"strings"
)

const runtimeVertices = 1024

type Vertex struct {
	Proto    string `json:"proto"`
	Addr     string `json:"addr"`
	Port     uint16 `json:"port"`
	Path     string `json:"path,omitempty"`
	Endpoint bool   `json:"endpoint"`
}

type Edge struct {
	From uint32 `json:"from"`
	To   uint32 `json:"to"`
}

func vertexID(owner uint16, counter uint32) uint32 {
	return uint32(owner)<<24 | counter&0xffffff
}

func vertexOwner(id uint32) uint16 {
	return uint16(id >> 24)
}

func vertexCounter(id uint32) uint32 {
	return id & 0xffffff
}

func hostID(owner uint16) uint32 {
	return vertexID(owner, 0)
}

func isHostID(id uint32) bool {
	return vertexCounter(id) == 0
}

func udpVertex(ip net.IP, port int) Vertex {
	if ip.To16() == nil {
		return Vertex{}
	}

	return Vertex{Proto: "udp", Addr: ip.String(), Port: uint16(port), Endpoint: port != 0}
}

func (e Vertex) ipv6() bool {
	ip := e.ip()

	return ip != nil && ip.To4() == nil
}

func (e Vertex) canonical() Vertex {
	e.Addr = strings.ToLower(e.Addr)

	if ip := net.ParseIP(e.Addr); ip != nil {
		e.Addr = ip.String()
	}

	if (e.Proto == "ws" || e.Proto == "wss") && e.Path == "" {
		e.Path = "/"
	}

	return e
}

func (e Vertex) valid() bool {
	if e.Addr == "" || e.ip().IsUnspecified() || strings.ContainsRune(e.Addr, 0) || strings.ContainsRune(e.Path, 0) {
		return false
	}

	if e.Proto == "udp" || e.Proto == "tcp" {
		return e.ip() != nil && !e.ip().IsLinkLocalUnicast() && e.Path == "" && (e.Port != 0 || (e.Proto == "udp" && !e.Endpoint)) && (e.Proto != "tcp" || !e.Endpoint)
	}

	return (e.Proto == "ws" || e.Proto == "wss") && e.Endpoint && e.Port != 0 && strings.HasPrefix(e.Path, "/")
}

func (e Vertex) ip() net.IP {
	return net.ParseIP(e.Addr)
}

func (e Vertex) string() string {
	address := net.JoinHostPort(e.Addr, strconv.Itoa(int(e.Port)))

	if e.Proto == "udp" || e.Proto == "tcp" {
		return address
	}

	return e.Proto + "://" + address + e.Path
}

func (e Vertex) url() string {
	path := throw2(url.ParseRequestURI(e.Path))

	path.Scheme, path.Host = e.Proto, net.JoinHostPort(e.Addr, strconv.Itoa(int(e.Port)))

	return path.String()
}

func compareVertex(a, b Vertex) int {
	return compareVertexKey(vertexKey(0, a), vertexKey(0, b))
}

func compareEdge(a, b Edge) int {
	if v := cmp.Compare(a.From, b.From); v != 0 {
		return v
	}

	return cmp.Compare(a.To, b.To)
}

func (v Vertex) isHost() bool {
	return v.Proto == "udp" && v.Port == 0
}

func (v Vertex) isEndpoint() bool {
	return v.Endpoint
}

func socketVertex(address *net.TCPAddr) Vertex {
	v := Vertex{Proto: "tcp", Addr: address.IP.String(), Port: uint16(address.Port)}

	v.Endpoint = false

	return v
}

func (v Vertex) endpoint() Endpoint {
	return Endpoint{Proto: v.Proto, Addr: v.Addr, Port: v.Port, Path: v.Path}
}

type Endpoint struct {
	Proto string `json:"proto"`
	Addr  string `json:"addr"`
	Port  uint16 `json:"port"`
	Path  string `json:"path,omitempty"`
}

func (e Endpoint) vertex() Vertex {
	return Vertex{Proto: e.Proto, Addr: e.Addr, Port: e.Port, Path: e.Path, Endpoint: true}
}

func (e Endpoint) valid() bool {
	return e.Port != 0 && e.vertex().isEndpoint() && e.vertex().valid()
}

func (e Endpoint) canonical() Endpoint {
	return e.vertex().canonical().endpoint()
}

func (e Endpoint) ip() net.IP {
	return e.vertex().ip()
}

func (e Endpoint) string() string {
	return e.vertex().string()
}

func (e Endpoint) url() string {
	return e.vertex().url()
}
