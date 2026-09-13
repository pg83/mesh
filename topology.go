package main

import (
	"context"
	"maps"
	"net/netip"
	"time"
)

func (n *Node) channel(edge Edge, peer uint16, outgoing bool) *Channel {
	actor := n.channels[edge]

	if actor == nil {
		actor = &Channel{node: n, edge: edge, peer: peer, outgoing: outgoing, inbox: newMailbox[any](nil),
			session: n.session(peer), packetID: uint64(time.Now().UnixNano())}
		n.channels[edge] = actor
		go n.loop("channel", actor.run)
	}

	return actor
}

func (n *Node) excludesDial(from, to Vertex) bool {
	if len(n.noDial) == 0 || from.Proto != "source" || from.Node != n.cfg.Index || !to.isEndpoint() {
		return false
	}

	src, _ := netip.ParseAddr(from.Addr)
	dst, _ := netip.ParseAddr(to.Addr)

	return n.noDial[DialPair{From: src.Unmap(), To: dst.Unmap()}]
}

func (n *Node) publishSnapshot() {
	n.recompute()

	enabled := map[Edge]bool{}

	for index, peer := range n.reg.byIndex {
		if index == n.cfg.Index {
			continue
		}

		for _, dst := range n.candidates(peer) {
			for src := range n.local {
				if n.addresses[src].Proto != "source" {
					continue
				}

				if ip := n.addresses[dst].ip(); ip != nil && n.local[src].address.ipv6() != (ip.To4() == nil) {
					continue
				}

				if n.excludesDial(n.addresses[src], n.addresses[dst]) {
					continue
				}

				edge := Edge{From: src, To: dst}

				n.channel(edge, index, true)
				enabled[edge] = true
			}
		}
	}

	channels := map[Edge]chan any{}

	for edge, actor := range n.channels {
		channels[edge] = actor.inbox.in
	}

	view := &Snapshot{registry: n.reg, graph: maps.Clone(n.graph), addresses: maps.Clone(n.addresses), local: maps.Clone(n.local), owners: maps.Clone(n.owners),
		routes: n.routes, channels: channels, enabled: enabled}

	view.gossip = n.advertisements()
	n.snapshot = view

	for _, actor := range n.channels {
		post(actor.inbox.in, any(view))
	}

	post(n.tunInbox.in, any(view))
}

func (n *Node) observe(r ChannelReport) {
	if r.session != n.session(r.peer) {
		return
	}

	if r.status == nil {
		if _, exists := n.observed[r.edge]; exists {
			delete(n.observed, r.edge)
			n.record(r.edge, false)
		}
	}

	if r.status != nil && !r.seen.IsZero() && time.Since(r.seen) < sessionTimeout && r.seen.After(n.observed[r.edge]) {
		_, exists := n.observed[r.edge]

		n.observed[r.edge] = r.seen
		n.owned[r.edge] = true

		if !exists {
			n.record(r.edge, true)
			n.log.Info("link up", "from", n.addresses[r.edge.From].string(), "to", n.addresses[r.edge.To].string())
		}
	}

	if r.status == nil {
		delete(n.channelStatus, r.edge)
	} else {
		n.channelStatus[r.edge] = *r.status
	}

	if r.dialing {
		n.dialing[r.edge] = true
	} else {
		delete(n.dialing, r.edge)
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
				n.local = n.scanLocal(v)
				n.refresh(time.Now())
				n.publishSnapshot()
				dirty = false
			case EdgeRecords:
				n.handleEdges(v)
				dirty = true
			case VertexRecords:
				for _, vertex := range v {
					n.remember(vertex)
				}

				dirty = true
			case RegistryRecords:
				if n.handleRegistry(v) {
					n.publishSnapshot()
					dirty = false
				}
			case ChannelReport:
				n.observe(v)
				dirty = true
			case *ChannelIO:
				local := v.edge.To

				if v.outgoing {
					local = v.edge.From
				}

				if n.local[local] == nil || n.session(v.peer) != v.session {
					v.stop()

					continue
				}

				n.remember(v.source)
				n.remember(v.target)

				actor := n.channel(v.edge, v.peer, v.outgoing)

				n.publishSnapshot()
				post(actor.inbox.in, any(v))
			case chan *Snapshot:
				post(v, n.snapshot)
			case chan *Status:
				post(v, n.status())
			}
		case now := <-ticker.C:
			n.refresh(now)

			n.publishSnapshot()
			dirty = false
		case <-updates.C:
			if dirty {
				n.publishSnapshot()
				dirty = false
			}
		}
	}
}

func (n *Node) currentSnapshot(ctx context.Context) *Snapshot {
	reply := make(chan *Snapshot, 1)

	select {
	case n.events.in <- reply:
	case <-ctx.Done():
		throw(ctx.Err())
	}

	select {
	case view := <-reply:
		return view
	case <-ctx.Done():
		throw(ctx.Err())

		return nil
	}
}
