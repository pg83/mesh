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
	// One of the probes could not be made at all, so what this says about the
	// interfaces is incomplete and must not stand for the usual lifetime.
	partial bool
}
