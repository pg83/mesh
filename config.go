package main

import (
	"encoding/json"
	"os"
)

type PeerConfig struct {
	Index  uint16   `json:"index"`
	Pub    string   `json:"pub"`
	Intip  string   `json:"intip"`
	Static []string `json:"static"`
}

type Config struct {
	Index    uint16       `json:"index"`
	Key      string       `json:"key"`
	Port     int          `json:"port"`
	Subnet   string       `json:"subnet"`
	Tun      string       `json:"tun"`
	Mtu      int          `json:"mtu"`
	Status   string       `json:"status"`
	Registry []PeerConfig `json:"registry"`
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
