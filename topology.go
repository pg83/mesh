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

	actor := &Channel{node: n, edge: c.edge, peer: c.peer, outgoing: c.outgoing, inbox: newMailbox[any](c.ctx.Done()), session: c.session, io: c}

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
		routes: n.routes, hops: n.hops, next: n.next, channels: channels, records: versions}

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
	updates := time.NewTicker(20 * time.Millisecond)

	defer ticker.Stop()

	defer updates.Stop()

	dirty := false

	for {
		select {
		case event := <-n.events.out:
			switch v := event.(type) {
			case InterfaceState:
				n.syncListeners(v)
				n.interfaces = v
				n.syncLocal()
				n.refresh(time.Now())
				n.publishSnapshot()
				dirty = false
			case *GraphRecord:
				n.handleRecord(v)
				dirty = true
			case *Vector:
				n.handleVector(v)
				dirty = true
			case RegistryRecords:
				if n.handleRegistry(v) {
					n.publishSnapshot()
					dirty = false
				}
			case ChannelReport:
				n.observe(v)
				dirty = true
			case DialResult:
				if n.dials[v.attempt.key] != v.attempt {
					for _, c := range v.channels {
						c.stop()
					}

					continue
				}

				v.attempt.pending = false
				v.attempt.channels = v.channels

				for _, c := range v.channels {
					n.installChannel(c)
				}

				n.refresh(time.Now())
				n.publishSnapshot()
			case *ChannelIO:
				n.installChannel(v)
				n.refresh(time.Now())
				n.publishSnapshot()

			case chan *Snapshot:
				post(v, n.snapshot)
			case chan *Status:
				if dirty {
					n.refresh(time.Now())
					n.publishSnapshot()
					dirty = false
				}

				post(v, n.status())
			}
		case now := <-ticker.C:
			n.refresh(now)

			n.publishSnapshot()
			dirty = false
		case <-updates.C:
			if dirty {
				n.refresh(time.Now())
				n.publishSnapshot()
				dirty = false
			}
		}
	}
}

func (n *Node) currentSnapshot() *Snapshot {
	reply := make(chan *Snapshot, 1)

	n.events.in <- reply

	return <-reply
}
