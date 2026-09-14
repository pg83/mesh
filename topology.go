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
	local := c.target

	if c.outgoing {
		local = c.source
	}

	if c.ctx.Err() != nil || n.session(c.peer) != c.session || (local.isEndpoint() && n.local[local.hash()] == nil) || (!local.isEndpoint() && (c.local == nil || !n.sourceAvailable(c.local))) {
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

	n.remember(c.source)
	n.remember(c.target)

	if !local.isEndpoint() {
		n.local[local.hash()] = c.local
	}

	actor := &Channel{node: n, edge: c.edge, peer: c.peer, outgoing: c.outgoing, inbox: newMailbox[any](c.ctx.Done()), session: c.session, packetID: c.id - 1, io: c}

	n.channels[c.edge] = actor
	n.channelStatus[c.edge] = ChannelStatus{Transport: c.transport(), Edge: c.edge, Outgoing: c.outgoing, ID: c.id}
	go n.loop("channel", actor.run)
}

func (n *Node) syncLocal() {
	n.local = n.scanLocal(n.interfaces)

	for _, actor := range n.channels {
		c := actor.io
		local := c.target

		if c.outgoing {
			local = c.source
		}

		if local.isEndpoint() {
			if n.local[local.hash()] == nil {
				c.stop()
			}
		} else if c.ctx.Err() == nil && n.sourceAvailable(c.local) {
			n.local[local.hash()] = c.local
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

	view := &Snapshot{registry: n.reg, graph: maps.Clone(n.graph), addresses: maps.Clone(n.addresses), local: maps.Clone(n.local), owners: maps.Clone(n.owners),
		routes: n.routes, channels: channels, records: versions}

	view.gossip = n.advertisements()
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
		delete(n.observed, r.edge)
	}

	if r.status != nil && !r.seen.IsZero() && time.Since(r.seen) < sessionTimeout && r.seen.After(n.observed[r.edge]) {
		_, exists := n.observed[r.edge]

		n.observed[r.edge] = r.seen

		if !exists {
			n.log.Info("link up", "from", n.addresses[r.edge.From].string(), "to", n.addresses[r.edge.To].string())
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
