package main

import "time"

const registryInterval = 5 * time.Second

func (a *Channel) exchangeRegistry(now time.Time) {
	if !a.outgoing || a.view == nil || !a.enabled() || now.Before(a.nextRegistry) {
		return
	}

	a.send(kindRegistry, a.view.registry.packet())
	a.nextRegistry = now.Add(registryInterval)
}

func (n *Node) handleRegistry(records RegistryRecords) bool {
	reg := n.reg

	for _, record := range records {
		old := reg.byIndex[record.Index]

		if old != nil && old.version >= record.Version {
			continue
		}

		try(func() {
			peer := newPeer(record.PeerConfig, record.Version)
			me := reg.byIndex[n.cfg.Index]

			if peer.index == n.cfg.Index {
				if string(peer.pub) != string(n.key.public) || peer.intip != me.intip {
					return
				}
			} else if old != nil && string(old.pub) == string(peer.pub) {
				peer.session = old.session
			} else {
				peer.session = newSession(me, peer, n.key.private)
			}

			if reg == n.reg {
				reg = reg.copy()
			}

			if old != nil && reg.byIntip[old.intip] == old {
				delete(reg.byIntip, old.intip)
			}

			reg.byIndex[peer.index] = peer
			reg.byIntip[peer.intip] = peer

			n.log.Info("registry updated", "index", peer.index, "version", peer.version)
		}).catch(func(e *Exception) { n.log.Debug("registry record rejected", "index", record.Index, "err", e) })
	}

	changed := reg != n.reg

	n.reg = reg

	return changed
}
