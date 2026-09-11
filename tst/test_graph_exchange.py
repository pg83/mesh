"""Gossip merges arbitrary endpoint pairs and relays preserve their versions."""
import time
import json
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
        captured = lab.intercept('r', 'a', 'hold', kind=4)
        probe.send(op='ad', body=dict(edges=[xy]))
        lab.wait(lambda: len(captured['held']) == 1, 'unsigned gossip captured')
        body_size = len(json.dumps(lib.wire_ad(dict(edges=[xy]))).encode())
        assert len(captured['held'][0][1]) == 20 + 8 + 35 + 16 + 1 + body_size
        lab.clear(captured)
        lab.replay(captured, transform=lambda packet: packet[:-1] + bytes([packet[-1] ^ 1]))
        probe.send(op='ad', body=dict(edges=[yz]))
        lab.wait(lambda: present('a', y, z), 'valid gossip after corrupted packet processed')
        assert not present('a', x, y), 'corrupted gossip bypassed transport authentication'
        incomplete = lib.wire_ad(dict(edges=[xy]))
        incomplete['endpoints'] = []
        probe.proc.stdin.write(json.dumps(dict(op='ad', body=incomplete)).encode() + b'\n')
        assert probe.read()['sent']
        time.sleep(.1)
        probe.send(op='ad', body=dict(edges=[xy]))
        lab.wait(lambda: present('a', x, y), 'same version accepted once descriptors arrive')
        probe.send(op='ad', body=dict(edges=[yz]))
        lab.wait(lambda: present('c', x, y) and present('c', y, z), 'third-party graph reaches another peer')
        assert not present('a', y, x), 'reverse edge was invented'
        assert x != y, 'ports distinguish graph vertices'
        # A partial batch does not remove pairs omitted from it.
        probe.send(op='ad', body=dict(edges=[dict(xy, id=ident+1)]))
        lab.wait(lambda: any(e['from'] == x and e['to'] == y and e['id'] == ident+1
                            for e in lab.status('c')['graph']), 'new pair version propagates')
        assert present('c', y, z), 'an omitted pair was removed'
        probe.send(op='ad', body=dict(edges=[dict(yz, id=ident+1, alive=False)]))
        lab.wait(lambda: not present('c', y, z), 'explicit pair withdrawal propagates')
        # A fresh record does not revive an older withdrawn pair in the same batch.
        probe.send(op='ad', body=dict(edges=[dict(xy, id=ident+2), yz]))
        lab.wait(lambda: any(e['from'] == x and e['to'] == y and e['id'] == ident+2
                            for e in lab.status('c')['graph']), 'new record in a mixed-version batch propagates')
        assert not present('c', y, z), 'mixed-version batch restored a superseded pair'
        burst = [lib.edge(lib.endpoint('192.0.2.10', 9000+i), z, ident+i+2) for i in range(24)]
        relays = lab.intercept('a', 'c', 'copy', kind=4, count=-1)
        held = lab.intercept('r', 'a', 'hold', kind=4, count=-1)
        for update in burst:
            probe.send(op='ad', body=dict(edges=[update]))
        probe.send(op='ad', body=dict(edges=[dict(burst[0], id=ident+100, alive=False)]))
        probe.send(op='ad', body=dict(edges=[dict(burst[0], id=ident+99)]))
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
