"""Gossip relays whole per-node records and every peer keeps only the newest version."""
import time
import lib
import workload


def test():
    with lib.Lab(['a', 'r', 'c'], {1: ['a', 'r', 'c']}) as lab:
        lab.wait_ping('a', 'c')
        probe = workload.Probe(lab, 'r', 'a')
        host = lib.endpoint(lib.intip(2), 0)
        x = lib.endpoint('10.1.0.2')
        y = lib.socket_vertex('10.1.0.2', 40001)
        z = lib.socket_vertex('10.1.0.2', 40002)
        ident = time.time_ns() + 1_000_000_000

        def present(name, src, dst):
            return any(e['from'] == src and e['to'] == dst for e in lab.status(name)['graph'])

        def version(name):
            return next((r['version'] for r in lab.status(name)['records'] if r['owner'] == 2), None)

        captured = lab.intercept('r', 'a', 'hold', kind=4)
        probe.send(op='graph', body=lib.record(2, ident, [(x, True, False)]))
        lab.wait(lambda: len(captured['held']) == 1, 'unsigned gossip captured')
        assert len(captured['held'][0][1]) == 20 + 8 + 15 + 16 + 7 + 11 + 2 + 8 + 2
        lab.clear(captured)
        lab.replay(captured, transform=lambda packet: packet[:-1] + bytes([packet[-1] ^ 1]))
        probe.send(op='graph', body=lib.record(2, ident + 1, [(x, True, False), (y, False, True)]))
        lab.wait(lambda: present('a', host, y), 'valid gossip after corrupted packet processed')
        assert version('a') == ident + 1, 'corrupted gossip bypassed transport authentication'
        assert present('a', x, host)
        lab.wait(lambda: present('c', x, host) and present('c', host, y), 'third-party record reaches another peer')
        assert not present('c', y, host), 'reverse edge was invented'
        assert version('c') == ident + 1, 'relay changed the record version'
        # A newer record replaces the whole vertex set of its owner.
        probe.send(op='graph', body=lib.record(2, ident + 2, [(x, True, False)]))
        lab.wait(lambda: version('c') == ident + 2, 'newer record propagates')
        assert not present('c', host, y), 'an omitted vertex survived a newer record'
        assert present('c', x, host)
        assert str(lib.endpoint_hash(y)) not in lab.status('c')['addresses']
        # An older version cannot roll a peer back.
        probe.send(op='graph', body=lib.record(2, ident + 1, [(x, True, False), (y, False, True)]))
        probe.send(op='graph', body=lib.record(2, ident + 3, [(x, True, False), (z, False, True)]))
        lab.wait(lambda: version('c') == ident + 3, 'barrier record propagates')
        assert not present('c', host, y), 'a superseded record was applied'
        # Links resolve only against the current record of their source owner.
        c_socket = lab.channel_source('c', '10.1.0.3', '10.1.0.2')
        probe.send(op='graph', body=lib.record(2, ident + 4, [(x, True, False)], [(c_socket, x), (y, x)]))
        lab.wait(lambda: present('a', c_socket, x), 'link with a known source vertex appears')
        assert not present('a', y, x), 'link with an unknown source vertex appeared'

        burst = [lib.record(2, ident + 10 + i, [(x, True, False), (lib.socket_vertex('10.1.0.2', 41000 + i), False, True)])
                 for i in range(24)]
        relays = lab.intercept('a', 'c', 'copy', kind=4, count=-1)
        held = lab.intercept('r', 'a', 'hold', kind=4, count=-1)
        for update in burst:
            probe.send(op='graph', body=update)
        probe.send(op='graph', body=lib.record(2, ident + 100, [(x, True, False)]))
        probe.send(op='graph', body=lib.record(2, ident + 99, [(x, True, False), (z, False, True)]))
        lab.wait(lambda: len(held['held']) == len(burst) + 2, 'all burst packets held')
        lab.clear(held)
        lab.release(held)
        lab.wait(lambda: version('c') == ident + 100, 'the latest record reaches another peer')
        assert not any(e['from'] == host and e['to']['port'] >= 40000 for e in lab.status('c')['graph']), 'stale sockets survived the burst'
        assert relays['held']
        assert max(len(packet) for _, packet, _ in relays['held']) <= 1200
        lab.wait_ping('a', 'c')
        probe.finish()


lib.main(test)
