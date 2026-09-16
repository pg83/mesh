package main

import (
	"net/netip"
	"time"
)

const routeTTL = 30 * time.Second

type Route struct {
	host    string
	targets []netip.Addr
	viable  map[int]bool
	at      time.Time
	pending bool
}
