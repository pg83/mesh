package main

import (
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type Vertex struct {
	Proto string `json:"proto"`
	Addr  string `json:"addr"`
	Port  uint16 `json:"port"`
	Path  string `json:"path,omitempty"`
	Node  uint16 `json:"node,omitempty"`
}

type Edge struct {
	From uint64 `json:"from"`
	To   uint64 `json:"to"`
}

func udpVertex(ip net.IP, port int) Vertex {
	if ip.To16() == nil {
		return Vertex{}
	}

	return Vertex{Proto: "udp", Addr: ip.String(), Port: uint16(port)}
}

func (e Vertex) ipv6() bool {
	ip := e.ip()

	return ip != nil && ip.To4() == nil
}

func (e Vertex) socketKey() SocketKey {
	return SocketKey{addr: e.Addr, port: e.Port, ipv6: e.ipv6()}
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

	if e.Proto == "source" {
		return e.Node != 0 && e.Port == 0 && e.Path == "" && e.ip() != nil
	}

	if e.Node != 0 {
		return false
	}

	if e.Proto == "udp" {
		return e.ip() != nil && !e.ip().IsLinkLocalUnicast() && e.Path == ""
	}

	return (e.Proto == "ws" || e.Proto == "wss") && e.Port != 0 && strings.HasPrefix(e.Path, "/")
}

func (e Vertex) hash() uint64 {
	if !e.valid() {
		return 0
	}

	prefix := ""

	if e.Proto == "source" {
		prefix = strconv.Itoa(int(e.Node)) + "\x00"
	}

	sum := sha256.Sum256([]byte(prefix + e.Proto + "\x00" + e.Addr + "\x00" + strconv.Itoa(int(e.Port)) + "\x00" + e.Path))

	return binary.LittleEndian.Uint64(sum[:8])
}

func (e Vertex) ip() net.IP {
	return net.ParseIP(e.Addr)
}

func (e Vertex) addr() *net.UDPAddr {
	return &net.UDPAddr{IP: e.ip(), Port: int(e.Port)}
}

func (e Vertex) string() string {
	if e.Proto == "source" {
		return "source:" + strconv.Itoa(int(e.Node)) + ":" + e.Addr
	}

	address := net.JoinHostPort(e.Addr, strconv.Itoa(int(e.Port)))

	if e.Proto == "udp" {
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
	if v := cmp.Compare(a.Proto, b.Proto); v != 0 {
		return v
	}

	ai, bi := a.ip().To4(), b.ip().To4()

	if ai != nil && bi != nil {
		for i := range ai {
			if v := int(ai[i]) - int(bi[i]); v != 0 {
				return v
			}
		}
	} else if v := cmp.Compare(a.Addr, b.Addr); v != 0 {
		return v
	}

	if v := cmp.Compare(a.Port, b.Port); v != 0 {
		return v
	}

	return cmp.Compare(a.Path, b.Path)
}

func compareEdge(a, b Edge) int {
	if v := cmp.Compare(a.From, b.From); v != 0 {
		return v
	}

	return cmp.Compare(a.To, b.To)
}

func (p *Peer) vertex() Vertex {
	return udpVertex(net.IP(p.intip[:]), 0)
}

func (n *Node) remember(e Vertex) uint64 {
	e = e.canonical()

	id := e.hash()

	if id == 0 {
		return 0
	}

	if old, ok := n.addresses[id]; ok && old != e {
		throwFmt("endpoint hash collision")
	}

	n.addresses[id] = e

	return id
}

func (v Vertex) isHost() bool {
	return v.Proto == "udp" && v.Port == 0
}

func (v Vertex) isEndpoint() bool {
	return v.Proto != "source" && !v.isHost()
}

func sourceVertex(node uint16, ip net.IP) Vertex {
	return Vertex{Proto: "source", Node: node, Addr: ip.String()}
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
	return Vertex{Proto: e.Proto, Addr: e.Addr, Port: e.Port, Path: e.Path}
}

func (e Endpoint) valid() bool {
	return e.Port != 0 && e.vertex().isEndpoint() && e.vertex().valid()
}

func (e Endpoint) canonical() Endpoint {
	return e.vertex().canonical().endpoint()
}

func (e Endpoint) hash() uint64 {
	if !e.valid() {
		return 0
	}

	return e.vertex().hash()
}

func (e Endpoint) ip() net.IP {
	return e.vertex().ip()
}

func (e Endpoint) ipv6() bool {
	return e.vertex().ipv6()
}

func (e Endpoint) socketKey() SocketKey {
	return e.vertex().socketKey()
}

func (e Endpoint) string() string {
	return e.vertex().string()
}

func (e Endpoint) url() string {
	return e.vertex().url()
}
