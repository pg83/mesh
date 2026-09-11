"""Gossip merges arbitrary endpoint pairs and relays preserve their versions."""
import time
import lib
import workload


def test():
    with lib.Lab(['a', 'r', 'c'], {1: ['a', 'r', 'c']}) as lab:
        lab.wait_ping('a', 'c')
        probe = workload.Probe(lab, 'r', 'a')
        x = lib.endpoint('192.0.2.1', 8000)
        y = lib.endpoint('192.0.2.1', 8001)
        z = lib.endpoint('192.0.2.2', 8000)
        ident = time.time_ns()
        xy = lib.edge(x, y, ident)
        yz = lib.edge(y, z, ident)
        def present(name, src, dst):
            return any(e['from'] == src and e['to'] == dst for e in lab.status(name)['graph'])
        probe.send(op='ad', body=dict(index=2, edges=[xy, yz]))
        lab.wait(lambda: present('c', x, y) and present('c', y, z), 'third-party graph reaches another peer')
        assert not present('a', y, x), 'reverse edge was invented'
        assert x != y, 'ports distinguish graph vertices'
        # A partial batch does not remove pairs omitted from it.
        probe.send(op='ad', body=dict(index=2, edges=[dict(xy, id=ident+1)]))
        lab.wait(lambda: any(e['from'] == x and e['to'] == y and e['id'] == ident+1
                            for e in lab.status('c')['graph']), 'new pair version propagates')
        assert present('c', y, z), 'an omitted pair was removed'
        probe.send(op='ad', body=dict(index=2, edges=[dict(yz, id=ident+1, alive=False)]))
        lab.wait(lambda: not present('c', y, z), 'explicit pair withdrawal propagates')
        # A different signer cannot make an older version supersede a withdrawal.
        probe.send(op='ad', body=dict(index=3, edges=[dict(xy, id=ident+2), yz]),
                   key=lab.nodes['c'].keys['key'])
        lab.wait(lambda: any(e['from'] == x and e['to'] == y and e['id'] == ident+2
                            for e in lab.status('c')['graph']), 'new version from another signer propagates')
        assert not present('c', y, z), 'another signer restored a superseded pair'
        burst = [lib.edge(lib.endpoint('192.0.2.10', 9000+i), z, ident+i+2) for i in range(24)]
        relays = lab.intercept('a', 'c', 'copy', kind=4, count=-1)
        held = lab.intercept('r', 'a', 'hold', kind=4, count=-1)
        for update in burst:
            probe.send(op='ad', body=dict(index=2, edges=[update]))
        probe.send(op='ad', body=dict(index=2, edges=[dict(burst[0], id=ident+100, alive=False)]))
        probe.send(op='ad', body=dict(index=2, edges=[dict(burst[0], id=ident+99)]))
        lab.wait(lambda: len(held['held']) == len(burst)+2, 'all burst packets held')
        lab.clear(held)
        lab.release(held)
        def relayed():
            graph = lab.status('c')['graph']
            pairs = all(any(e['from'] == u['from'] and e['to'] == u['to'] and e['id'] == u['id']
                            for e in graph) for u in burst[1:])
            withdrawn = not any(e['from'] == burst[0]['from'] and e['to'] == z for e in graph)
            return pairs and withdrawn
        lab.wait(relayed, 'all burst pairs and the latest withdrawal reach another peer')
        assert relays['held']
        assert max(len(packet) for _, packet, _ in relays['held']) <= 1200
        lab.wait_ping('a', 'c')
        probe.finish()


lib.main(test)
