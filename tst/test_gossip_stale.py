"""13: Old graph records stay obsolete even when another peer republishes them."""
import time
import lib
import workload


def test():
    with lib.Lab(['a', 'r', 'b'], {1: ['a', 'r'], 2: ['r', 'b']}) as lab:
        lab.wait_nodes('a', ['a', 'r', 'b'])
        lab.stop_node('b')
        probe = workload.Probe(lab, 'r', 'a')
        ident = time.time_ns() + 1_000_000_000
        mesh = lib.endpoint(lib.intip(3), 0)
        endpoints = [lib.endpoint('10.2.0.3'), lib.source('10.2.0.3', 3)]
        old = [edge for ep in endpoints for edge in [lib.edge(mesh, ep, ident), lib.edge(ep, mesh, ident)]]
        probe.send(op='ad', body=dict(edges=old))
        withdrawn = [dict(e, id=ident + 1, alive=False) for e in old]
        probe.send(op='ad', body=dict(edges=withdrawn))
        lab.wait(lambda: 3 not in lab.known_nodes('a'), 'withdrawn endpoint disappears')
        # A new envelope and transport ID cannot make these old pair versions fresh.
        probe.send(op='ad', body=dict(edges=old))
        time.sleep(.2)
        assert 3 not in lab.known_nodes('a'), 'superseded graph records returned'
        assert lab.route('a', 'b') is None
        probe.finish()


lib.main(test)
