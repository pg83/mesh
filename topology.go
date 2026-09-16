package main

import (
	"maps"
	"time"
)

func (n *Node) sourceAvailable(local *LocalAddress) bool {
	for _, addr := range n.interfaces {
		if addr.ip == local.address.Addr && addr.iface == local.iface {
			return true
		}
	}

	return false
}

func (n *Node) installChannel(c *ChannelIO) {
	local := c.localID()

	if c.ctx.Err() != nil || n.session(c.peer) != c.session || (c.listener && n.local[local] == nil) || (!c.listener && (c.local == nil || !n.sourceAvailable(c.local))) {
		c.stop()

		return
	}

	old := n.channels[c.edge]

	if old != nil {
		previous := old.io

		if previous.ctx.Err() == nil && (previous.origin < c.origin || (previous.origin == c.origin && previous.id >= c.id)) {
			c.stop()

			return
		}

		previous.stop()
	}

	if c.source.valid() {
		n.addresses[c.edge.From] = c.source
	}

	if c.target.valid() {
		n.addresses[c.edge.To] = c.target
	}

	if !c.listener {
		n.local[local] = c.local
	}

	actor := &Channel{node: n, edge: c.edge, peer: c.peer, outgoing: c.outgoing, session: c.session, io: c, view: n.snapshot}

	n.channels[c.edge] = actor
	n.channelStatus[c.edge] = ChannelStatus{Transport: c.transport(), Edge: c.edge, Outgoing: c.outgoing, ID: c.id, Wire: c.wire}
	go n.loop("channel", actor.run)
}

func (n *Node) syncLocal() {
	n.local = n.scanLocal(n.interfaces)

	for _, actor := range n.channels {
		c := actor.io
		local := c.localID()

		if c.listener {
			if n.local[local] == nil {
				c.stop()
			}
		} else if c.ctx.Err() == nil && n.sourceAvailable(c.local) {
			n.local[local] = c.local
		} else {
			c.stop()
		}
	}
}

func (n *Node) publishSnapshot() {
	n.recompute()

	n.syncDials()

	channels := maps.Clone(n.channels)
	versions := map[uint16]uint64{}

	for owner, record := range n.records {
		versions[owner] = record.Version
	}

	view := &Snapshot{registry: n.reg, graph: maps.Clone(n.graph), addresses: maps.Clone(n.addresses), local: maps.Clone(n.local),
		routes: n.routes, hops: n.hops, next: n.next, channels: channels, records: versions, alive: n.alive}

	view.exits = map[uint16]bool{}

	for owner, record := range n.records {
		if record.Exit && n.alive[owner] {
			view.exits[owner] = true
		}
	}

	n.publishVector()
	view.vectors = maps.Clone(n.vectors)
	view.gossip = n.advertisements()
	view.bundles = n.bundles()

	n.snapshot = view

	for _, actor := range n.channels {
		actor.post(view)
	}

	post(n.tunInbox.in, any(view))
}

func (n *Node) observe(r ChannelReport) {
	if n.channels[r.edge] != r.actor || r.session != n.session(r.peer) {
		return
	}

	if r.status == nil {
		if _, exists := n.observed[r.edge]; exists {
			n.metrics.linkDown.Add(1)
		}

		delete(n.observed, r.edge)
	}

	if r.status != nil && !r.seen.IsZero() && time.Since(r.seen) < sessionTimeout && r.seen.After(n.observed[r.edge]) {
		_, exists := n.observed[r.edge]

		n.observed[r.edge] = r.seen

		if !exists {
			n.metrics.linkUp.Add(1)
			n.log.Info("link up", "from", n.describe(r.edge.From), "to", n.describe(r.edge.To))
			n.resetBackoff(r.peer)
		}
	}

	if r.status == nil {
		delete(n.channelStatus, r.edge)
		delete(n.channels, r.edge)
	} else {
		n.channelStatus[r.edge] = *r.status
	}
}

func (n *Node) graphLoop() {
	ticker := time.NewTicker(tickInterval)

	defer ticker.Stop()

	for {
		select {
		case event := <-n.events.out:
			switch v := event.(type) {
			case InterfaceState:
				n.syncListeners(v)
				n.interfaces = v
				clear(n.routeCache)
				n.resetBackoff(0)
				n.syncLocal()
			case *GraphRecord:
				n.handleRecord(v)
			case *Vector:
				n.handleVector(v)
			case *Route:
				n.routeCache[v.host] = v
			case RegistryRecords:
				n.handleRegistry(v)
			case ChannelReport:
				n.observe(v)
			case DialResult:
				if n.dials[v.attempt.key] != v.attempt {
					for _, c := range v.channels {
						c.stop()
					}

					continue
				}

				v.attempt.pending = false
				v.attempt.channels = v.channels

				if len(v.channels) == 0 {
					v.attempt.failures++
					v.attempt.next = time.Now().Add(backoff(v.attempt.failures))
				} else {
					n.resetBackoff(v.attempt.session.peer)
				}

				for _, c := range v.channels {
					n.installChannel(c)
				}
			case *ChannelIO:
				n.installChannel(v)
			case chan *Snapshot:
				post(v, n.snapshot)
			case chan *Status:
				post(v, n.status())
			}
		case now := <-ticker.C:
			n.refresh(now)
			n.publishSnapshot()
		}
	}
}

func (n *Node) currentSnapshot() *Snapshot {
	reply := make(chan *Snapshot, 1)

	n.events.in <- reply

	return <-reply
}
