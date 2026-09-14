"""A packet ID already accepted within the channel's window is dropped, so replays cannot refresh a link."""
import time
import lib
import workload


def rejected(lab, name):
    for line in lab.http(name, '/metrics')[2].decode().splitlines():
        if line.startswith('mesh_packets_rejected_total{reason="replay"}'):
            return float(line.split()[1])
    raise KeyError('replay counter')


def test():
    with lib.Lab(['a', 'b', 'r'], {1: ['a', 'b', 'r']}) as lab:
        lab.wait_ping('a', 'b')
        lab.wait_links('b', ['a', 'r'])
        assert rejected(lab, 'b') == 0
        captured = lab.intercept('a', 'b', 'copy', kind=4, count=4)
        lab.wait(lambda: captured['hits'] == 4, 'live gossip captured')
        lab.clear(captured)
        # A replayed packet is dropped by the live channel.
        lab.replay(captured)
        lab.wait(lambda: rejected(lab, 'b') == 4, 'replayed packets rejected by the channel')
        # Replays do not count as traffic: with live packets blocked the link expires.
        lab.block('a', 'b', both=False)
        for _ in range(3):
            lab.replay(captured)
            time.sleep(1)
        lab.wait_links('b', ['r'])
        assert rejected(lab, 'b') == 16
        lab.unblock('a', 'b', both=False)
        lab.wait_links('b', ['a', 'r'])
        lab.wait_ping('a', 'b')

        # Explicit IDs: a repeated ID is dropped, an unseen lower ID inside the window is
        # accepted as reordering, an ID below the window is dropped.
        probe = workload.Probe(lab, 'r', 'b')
        ident = time.time_ns() + 1_000_000_000
        x = lib.endpoint('192.0.2.7', 9000)

        def present(version):
            return any(r['owner'] == 3 and r['version'] == version for r in lab.status('b')['records'])

        probe.send(op='graph', body=lib.record(3, ident, [(x, True, False)]), id=ident)
        lab.wait(lambda: present(ident), 'first packet accepted')
        before = rejected(lab, 'b')
        probe.send(op='graph', body=lib.record(3, ident + 1, [(x, True, False)]), id=ident)
        probe.send(op='graph', body=lib.record(3, ident + 2, [(x, True, False)]), id=ident - 5)
        lab.wait(lambda: present(ident + 2), 'reordered packet inside the window accepted')
        probe.send(op='graph', body=lib.record(3, ident + 3, [(x, True, False)]), id=ident - 5)
        probe.send(op='graph', body=lib.record(3, ident + 4, [(x, True, False)]), id=ident - 3000)
        probe.send(op='graph', body=lib.record(3, ident + 5, [(x, True, False)]), id=ident + 1)
        lab.wait(lambda: present(ident + 5), 'higher packet id accepted')
        assert not any(present(ident + v) for v in (1, 3, 4)), 'a replayed or expired packet id was accepted'
        assert rejected(lab, 'b') == before + 3
        probe.finish()


lib.main(test)
