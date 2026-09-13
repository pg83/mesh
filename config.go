package main

import (
	"encoding/json"
	"os"
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

type Config struct {
	RegistryVersion uint64           `json:"registry_version,omitempty"`
	Index           uint16           `json:"index"`
	Key             string           `json:"key,omitempty"`
	Endpoint        []EndpointConfig `json:"endpoint"`
	Subnet          string           `json:"subnet"`
	Tun             string           `json:"tun,omitempty"`
	Mtu             int              `json:"mtu"`
	Control         string           `json:"control,omitempty"`
	Registry        []PeerConfig     `json:"registry"`
}

func loadConfig(path string) *Config {
	data := throw2(os.ReadFile(path))
	cfg := &Config{}

	throw(json.Unmarshal(data, cfg))

	if cfg.Mtu == 0 {
		cfg.Mtu = 1380
	}

	if cfg.Tun == "" {
		cfg.Tun = defaultTun
	}

	return cfg
}
