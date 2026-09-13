package main

import (
	"context"
	"maps"
	"net/netip"
	"time"
)

func (n *Node) edgeActor(edge Edge, peer uint16, outgoing bool) *EdgeActor {
	actor := n.actors[edge]

	if actor == nil {
		actor = &EdgeActor{node: n, edge: edge, peer: peer, outgoing: outgoing, inbox: newMailbox[any](nil),
			session: n.session(peer), packetID: uint64(time.Now().UnixNano())}
		n.actors[edge] = actor
		go n.loop("edge", actor.run)
	}

	return actor
}

func (n *Node) edgePair(edge Edge, peer uint16) *EdgeActor {
	actor := n.edgeActor(edge, peer, true)

	n.edgeActor(Edge{From: edge.To, To: edge.From}, peer, false)

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

				n.edgePair(edge, index)
				enabled[edge] = true
			}
		}
	}

	actors := map[Edge]chan any{}

	for edge, actor := range n.actors {
		actors[edge] = actor.inbox.in
	}

	view := &Snapshot{registry: n.reg, graph: maps.Clone(n.graph), addresses: maps.Clone(n.addresses), local: maps.Clone(n.local), owners: maps.Clone(n.owners),
		routes: n.routes, actors: actors, enabled: enabled}

	view.gossip = n.advertisements()
	n.snapshot = view

	for _, actor := range n.actors {
		post(actor.inbox.in, any(view))
	}

	post(n.tunInbox.in, any(view))
}

func (n *Node) observe(r EdgeReport) {
	if r.session != n.session(r.peer) {
		return
	}

	if !r.seen.IsZero() && time.Since(r.seen) < sessionTimeout && r.seen.After(n.observed[r.edge]) {
		_, exists := n.observed[r.edge]

		n.observed[r.edge] = r.seen
		n.owned[r.edge] = true

		if !exists {
			n.record(r.edge, true)
			n.log.Info("link up", "from", n.addresses[r.edge.From].string(), "to", n.addresses[r.edge.To].string())
		}
	}

	if r.connection == nil {
		delete(n.connection, r.edge)
	} else {
		n.connection[r.edge] = *r.connection
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
			case *Ad:
				n.handleAd(v)
				dirty = true
			case RegistryRecords:
				if n.handleRegistry(v) {
					n.publishSnapshot()
					dirty = false
				}
			case EdgeReport:
				n.observe(v)
				dirty = true
			case *Connection:
				if n.local[v.edge.From] == nil || n.session(v.peer) != v.session {
					v.stop()
					v.conn.close()

					continue
				}

				n.remember(v.source)
				n.remember(v.target)

				actor := n.edgePair(v.edge, v.peer)

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
