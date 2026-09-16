"""13: Old graph records stay obsolete even when another peer republishes them."""
import time
import lib
import work_load as workload


def test():
    with lib.Lab(['a', 'r', 'b'], {1: ['a', 'r'], 2: ['r', 'b']}) as lab:
        lab.wait_nodes('a', ['a', 'r', 'b'])
        current = next(r for r in lab.status('a')['records'] if r['owner'] == 3)
        lab.stop_node('b')
        probe = workload.Probe(lab, 'r', 'a')
        ident = time.time_ns() + 1_000_000_000
        old = dict(current, version=ident)
        probe.send(op='graph', body=old)
        probe.send(op='graph', body=dict(old, version=ident + 1, vertices=[], links=[]))
        lab.wait(lambda: 3 not in lab.known_nodes('a'), 'withdrawn endpoint disappears')
        # A new envelope and transport ID cannot make this old record version fresh.
        probe.send(op='graph', body=old)
        time.sleep(.2)
        assert 3 not in lab.known_nodes('a'), 'superseded graph records returned'
        assert lab.route('a', 'b') is None
        probe.finish()


lib.main(test)
