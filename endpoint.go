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

type Endpoint struct {
	Proto string `json:"proto"`
	Addr  string `json:"addr"`
	Port  uint16 `json:"port"`
	Path  string `json:"path,omitempty"`
}

type Edge struct {
	From uint64 `json:"from"`
	To   uint64 `json:"to"`
}

func endpoint(ip net.IP, port int) Endpoint {
	if ip.To4() == nil {
		return Endpoint{}
	}

	return Endpoint{Proto: "udp", Addr: ip.To4().String(), Port: uint16(port)}
}

func (e Endpoint) canonical() Endpoint {
	e.Addr = strings.ToLower(e.Addr)

	if ip := net.ParseIP(e.Addr); ip != nil {
		e.Addr = ip.String()
	}

	if (e.Proto == "ws" || e.Proto == "wss") && e.Path == "" {
		e.Path = "/"
	}

	return e
}

func (e Endpoint) valid() bool {
	if e.Addr == "" || e.Addr == "0.0.0.0" || strings.ContainsRune(e.Addr, 0) || strings.ContainsRune(e.Path, 0) {
		return false
	}

	if e.Proto == "udp" {
		return net.ParseIP(e.Addr).To4() != nil && e.Path == ""
	}

	return (e.Proto == "ws" || e.Proto == "wss") && e.Port != 0 && strings.HasPrefix(e.Path, "/")
}

func (e Endpoint) hash() uint64 {
	if !e.valid() {
		return 0
	}

	sum := sha256.Sum256([]byte(e.Proto + "\x00" + e.Addr + "\x00" + strconv.Itoa(int(e.Port)) + "\x00" + e.Path))

	return binary.LittleEndian.Uint64(sum[:8])
}

func (e Endpoint) ip() net.IP {
	return net.ParseIP(e.Addr)
}

func (e Endpoint) addr() *net.UDPAddr {
	return &net.UDPAddr{IP: e.ip(), Port: int(e.Port)}
}

func (e Endpoint) string() string {
	address := net.JoinHostPort(e.Addr, strconv.Itoa(int(e.Port)))

	if e.Proto == "udp" {
		return address
	}

	return e.Proto + "://" + address + e.Path
}

func (e Endpoint) url() string {
	path := throw2(url.ParseRequestURI(e.Path))

	path.Scheme, path.Host = e.Proto, net.JoinHostPort(e.Addr, strconv.Itoa(int(e.Port)))

	return path.String()
}

func compareEndpoint(a, b Endpoint) int {
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

func (p *Peer) endpoint() Endpoint {
	return endpoint(net.IP(p.intip[:]), 0)
}

func (n *Node) remember(e Endpoint) uint64 {
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
