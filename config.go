package main

import (
	"encoding/json"
	"os"
)

type EndpointConfig struct {
	Proto    string `json:"proto"`
	Addr     string `json:"addr"`
	Port     int    `json:"port"`
	BindAddr string `json:"bind_addr,omitempty"`
	BindPort int    `json:"bind_port,omitempty"`
	Path     string `json:"path,omitempty"`
}

type PeerConfig struct {
	Index    uint16           `json:"index"`
	Pub      string           `json:"pub"`
	Sig      string           `json:"sig"`
	Intip    string           `json:"intip"`
	Endpoint []EndpointConfig `json:"endpoint"`
}

type Config struct {
	Index    uint16           `json:"index"`
	Key      string           `json:"key"`
	Endpoint []EndpointConfig `json:"endpoint"`
	Subnet   string           `json:"subnet"`
	Tun      string           `json:"tun"`
	Mtu      int              `json:"mtu"`
	Status   string           `json:"status"`
	Registry []PeerConfig     `json:"registry"`
}

func loadConfig(path string) *Config {
	data := throw2(os.ReadFile(path))
	cfg := &Config{}

	throw(json.Unmarshal(data, cfg))

	if cfg.Mtu == 0 {
		cfg.Mtu = 1380
	}

	if cfg.Tun == "" {
		cfg.Tun = "mesh0"
	}

	return cfg
}
