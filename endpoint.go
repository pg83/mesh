package main

import (
	"encoding/binary"
	"net"
	"strconv"
)

type Endpoint struct {
	IP   uint32 `json:"ip"`
	Port uint16 `json:"port"`
}

type Edge struct {
	From Endpoint `json:"from"`
	To   Endpoint `json:"to"`
}

func endpoint(ip net.IP, port int) Endpoint {
	v := ip.To4()

	if v == nil {
		return Endpoint{}
	}

	return Endpoint{IP: binary.LittleEndian.Uint32(v), Port: uint16(port)}
}

func (e Endpoint) ip() net.IP {
	return net.IPv4(byte(e.IP), byte(e.IP>>8), byte(e.IP>>16), byte(e.IP>>24))
}

func (e Endpoint) addr() *net.UDPAddr {
	return &net.UDPAddr{IP: e.ip(), Port: int(e.Port)}
}

func (e Endpoint) string() string {
	return net.JoinHostPort(e.ip().String(), strconv.Itoa(int(e.Port)))
}

func compareEndpoint(a, b Endpoint) int {
	for shift := 0; shift < 32; shift += 8 {
		if x, y := byte(a.IP>>shift), byte(b.IP>>shift); x != y {
			return int(x) - int(y)
		}
	}

	return int(a.Port) - int(b.Port)
}

func compareEdge(a, b Edge) int {
	if order := compareEndpoint(a.From, b.From); order != 0 {
		return order
	}

	return compareEndpoint(a.To, b.To)
}

func (p *Peer) endpoint() Endpoint {
	return endpoint(net.IP(p.intip[:]), 0)
}
