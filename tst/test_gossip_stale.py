"""13: Old graph records stay obsolete even when another peer republishes them."""
import time
import lib
import workload


def test():
    with lib.Lab(['a', 'r', 'b'], {1: ['a', 'r'], 2: ['r', 'b']}) as lab:
        lab.wait_nodes('a', ['a', 'r', 'b'])
        mesh = lib.endpoint(lib.intip(3), 0)
        attachments = [edge for edge in lab.status('b')['graph'] if mesh in (edge['from'], edge['to'])]
        lab.stop_node('b')
        probe = workload.Probe(lab, 'r', 'a')
        ident = time.time_ns() + 1_000_000_000
        old = [dict(edge, id=ident) for edge in attachments]
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
