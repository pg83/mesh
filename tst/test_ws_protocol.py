"""An upgraded HTTP connection must authenticate its mesh identity and both endpoints."""
import json
import time
import lib
import ws


def test():
    with ws.Lab(names=['a', 'b', 'c']) as lab:
        lab.wait_ping('b', 'c')
        lab.wait(lambda: lab.known_nodes('b') == [1, 2, 3], 'endpoint owners known')
        lab.stop_node('a')
        a = lib.source('10.1.0.1', 1)
        b, c = [dict(proto='ws', addr=f'10.1.0.{i}', port=7100, path='/mesh') for i in (2, 3)]
        cases = [dict(op='raw', hex='00'), dict(op='raw', hex='03' + '00' * 50),
                 dict(op='raw', hex='030100' + '00' * 32),
                 dict(op='inner', hex=''), dict(op='inner', hex='ff'), dict(op='inner', hex='037b'),
                 dict(op='binding', body={'from': lib.endpoint('10.1.0.1'), 'to': b}),
                 dict(op='binding', body={'from': c, 'to': b}),
                 dict(op='binding', body={'from': a, 'to': dict(b, node=2)}),
                 dict(op='binding', body={'from': dict(a, addr='0.0.0.0'), 'to': b}),
                 dict(op='binding', body={'from': a, 'to': dict(b, path='/wrong')}),
                 dict(op='binding', body={'from': a, 'to': b}, text=True)]
        for command in cases:
            probe = ws.Probe(lab)
            assert probe.send(**command, read=True)['closed'], command
            probe.finish()
        bad = json.loads((lab.dir / 'a.json').read_text())
        bad['key'] = lab.nodes['c'].keys['key']
        path = lab.dir / 'wrong-key.json'
        path.write_text(json.dumps(bad))
        probe = ws.Probe(lab, config=path)
        assert probe.send(op='binding', body={'from': a, 'to': b}, read=True)['closed']
        probe.finish()
        probe = ws.Probe(lab)
        assert not probe.send(op='binding', body={'from': a, 'to': b}, read=True)['closed']
        assert probe.send(op='raw', hex='00', text=True, read=True)['closed']
        probe.finish()

        # A stale connection attempt must not replace the established channel
        # for the same source/endpoint pair, even with a valid key and binding.
        first = ws.Probe(lab)
        ident = time.time_ns() + 1_000_000_000
        assert not first.send(op='binding', body={'from': a, 'to': b}, id=ident, read=True)['closed']
        def connected_id():
            return [connection['id'] for connection in lab.status('b')['connections']
                    if connection['origin'] == lib.endpoint_hash(a)]
        lab.wait(lambda: connected_id() == [ident], 'new authenticated connection installed')
        stale = ws.Probe(lab)
        stale.send(op='binding', body={'from': a, 'to': b}, id=ident - 1, read=True)
        assert stale.send(op='read', read=True)['closed'], 'stale connection remained open'
        assert connected_id() == [ident], 'stale connection replaced the live channel'
        stale.finish()
        assert not first.send(op='binding', body={'from': a, 'to': b}, read=True)['closed']
        first.finish()
        lab.wait_ping('b', 'c')


lib.main(test)
