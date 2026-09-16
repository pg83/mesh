package main

import (
	"encoding/json"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type EndpointConfig struct {
	Proto     string `json:"proto"`
	Addr      string `json:"addr"`
	Port      int    `json:"port"`
	BindAddr  string `json:"bind_addr,omitempty"`
	BindPort  int    `json:"bind_port,omitempty"`
	Path      string `json:"path,omitempty"`
	BindProto string `json:"bind_proto,omitempty"`
	TLSCert   string `json:"tls_cert,omitempty"`
	TLSKey    string `json:"tls_key,omitempty"`
	TLSCA     string `json:"tls_ca,omitempty"`
}

type PeerConfig struct {
	Name     string           `json:"name,omitempty"`
	Index    uint16           `json:"index"`
	Pub      string           `json:"pub"`
	Intip    string           `json:"intip"`
	Endpoint []EndpointConfig `json:"endpoint"`
}

type DialPair struct {
	From netip.Addr `json:"from"`
	To   netip.Addr `json:"to"`
}

type Config struct {
	RegistryVersion    uint64              `json:"registry_version,omitempty"`
	Index              uint16              `json:"index"`
	Key                string              `json:"key,omitempty"`
	Endpoint           []EndpointConfig    `json:"endpoint"`
	Subnet             string              `json:"subnet"`
	Tun                string              `json:"tun,omitempty"`
	Mtu                int                 `json:"mtu"`
	Control            string              `json:"control,omitempty"`
	Registry           []PeerConfig        `json:"registry"`
	NoDial             []DialPair          `json:"no_dial,omitempty"`
	Sshd               bool                `json:"sshd,omitempty"`
	SshdPort           int                 `json:"sshd_port,omitempty"`
	SshdAuthorizedKeys string              `json:"sshd_authorized_keys,omitempty"`
	Dns                bool                `json:"dns,omitempty"`
	DnsPort            int                 `json:"dns_port,omitempty"`
	DnsRecords         map[string][]string `json:"dns_records,omitempty"`
	Exit               bool                `json:"exit,omitempty"`
	Routes             map[string][]string `json:"routes,omitempty"`
	exitRoutes         []ExitRoute
}

func loadConfig(path string) *Config {
	data := throw2(os.ReadFile(path))
	cfg := &Config{}

	throw(json.Unmarshal(data, cfg))

	for i, pair := range cfg.NoDial {
		if !pair.From.IsValid() || !pair.To.IsValid() {
			throwFmt("no_dial requires from and to IP addresses")
		}

		cfg.NoDial[i] = DialPair{From: pair.From.Unmap(), To: pair.To.Unmap()}
	}

	if cfg.Mtu == 0 {
		cfg.Mtu = 1380
	}

	cfg.exitRoutes = parseRoutes(cfg)
	cfg.DnsRecords = parseDNSRecords(cfg.DnsRecords)

	return cfg
}

func (c EndpointConfig) validate() {
	if c.Proto != "udp" && c.Proto != "ws" && c.Proto != "wss" {
		throwFmt("bad endpoint proto %q", c.Proto)
	}

	if c.Port < 1 || c.Port > 65535 || c.BindPort < 0 || c.BindPort > 65535 {
		throwFmt("bad endpoint port")
	}

	if c.Proto != "udp" {
		path := c.description().Path

		if !strings.HasPrefix(path, "/") {
			throwFmt("bad websocket path")
		}

		if _, err := url.ParseRequestURI(path); err != nil {
			throwFmt("bad websocket path: %s", err)
		}
	}
}

func (c EndpointConfig) address() *net.UDPAddr {
	return parseUDPAddr(net.JoinHostPort(c.Addr, strconv.Itoa(c.Port)))
}

func (c EndpointConfig) binding() *net.UDPAddr {
	addr, port := c.BindAddr, c.BindPort

	if addr == "" {
		addr = c.Addr
	}

	if port == 0 {
		port = c.Port
	}

	return parseUDPAddr(net.JoinHostPort(addr, strconv.Itoa(port)))
}

func (c EndpointConfig) description() Endpoint {
	if c.Proto == "udp" {
		a := c.address()

		return udpVertex(a.IP, a.Port).endpoint()
	}

	return (Endpoint{Proto: c.Proto, Addr: c.Addr, Port: uint16(c.Port), Path: c.Path}).canonical()
}
