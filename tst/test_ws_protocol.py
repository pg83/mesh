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
        cases = [dict(op='raw', hex='00'), dict(op='raw', hex='03' + '00' * 50),
                 dict(op='raw', hex='00' * 8 + '01' + '00' * 32), dict(op='inner', hex='00'),
                 # The source must be a vertex of the sender and not its mesh IP.
                 dict(op='graph', body=lib.record(1, time.time_ns(), []), source=lib.vertex_id(1, 0)),
                 dict(op='graph', body=lib.record(1, time.time_ns(), []), source=lib.vertex_id(3, 1)),
                 dict(op='graph', body=lib.record(1, time.time_ns(), []), source=lib.vertex_id(2, 1025)),
                 dict(op='graph', body=lib.record(1, time.time_ns(), []), source=0),
                 dict(op='graph', body=lib.record(1, time.time_ns(), []), text=True)]
        for command in cases:
            probe = ws.Probe(lab)
            assert probe.send(**command, read=True)['closed'], command
            probe.finish()
        bad = json.loads((lab.dir / 'a.json').read_text())
        bad['key'] = lab.nodes['c'].keys['key']
        path = lab.dir / 'wrong-key.json'
        path.write_text(json.dumps(bad))
        probe = ws.Probe(lab, config=path)
        assert probe.send(op='graph', body=lib.record(1, time.time_ns(), []), read=True)['closed']
        probe.finish()
        probe = ws.Probe(lab)
        assert not probe.send(op='graph', body=lib.record(1, time.time_ns(), []), read=True)['closed']
        a = probe.source
        # Frames on an established channel are checked like the first one.
        header = '00' * 8 + '01'
        for frame in ['00', '03' + header[2:] + '00' * 16, header[:16] + '03' + '00' * 16, header + 'ff' * 32]:
            probe.send(op='raw', hex=frame)
        probe.send(op='graph', body=lib.record(1, time.time_ns(), []), source=lib.vertex_id(1, 2000))
        assert not probe.send(op='read', read=True)['closed'], 'rejected frames closed the channel'
        assert any(c['from'] == a and not c['outgoing'] for c in lab.status('b')['channels'])
        rejected = [line for line in lab.http('b', '/metrics')[2].decode().splitlines() if line.startswith('mesh_packets_rejected_total')]
        assert [line.rsplit(' ', 1)[1] for line in rejected] == ['1', '1', '2', '1', '0'], rejected
        probe.send(op='raw', hex='00', text=True)
        lab.wait(lambda: not any(c['from'] == a and not c['outgoing']
                                for c in lab.status('b')['channels']), 'only incoming channel closed')
        assert any(c['to'] == a and c['outgoing'] for c in lab.status('b')['channels'])
        assert not probe.send(op='read', read=True)['closed'], 'reverse channel stopped after invalid input'
        probe.finish()

        # A stale connection attempt must not replace the established channel
        # for the same source/endpoint pair, even with a valid key and source header.
        first = ws.Probe(lab)
        a = first.source
        ident = time.time_ns() + 1_000_000_000
        assert not first.send(op='graph', body=lib.record(1, time.time_ns(), []), id=ident, read=True)['closed']
        def connected_id():
            return [connection['id'] for connection in lab.status('b')['channels']
                    if connection['from'] == a and not connection['outgoing']]
        lab.wait(lambda: connected_id() == [ident], 'new authenticated connection installed')
        stale = ws.Probe(lab)
        stale.send(op='graph', body=lib.record(1, time.time_ns(), []), source=a, id=ident - 1, read=True)
        assert stale.send(op='read', read=True)['closed'], 'stale connection remained open'
        assert connected_id() == [ident], 'stale connection replaced the live channel'
        stale.finish()
        assert not first.send(op='graph', body=lib.record(1, time.time_ns(), []), read=True)['closed']
        # A newer attempt for the same pair replaces the established channels.
        newer = ws.Probe(lab)
        newer.send(op='graph', body=lib.record(1, time.time_ns(), []), source=a, id=ident + 1, read=True)
        lab.wait(lambda: connected_id() == [ident + 1], 'newer connection replaced the channel')
        lab.wait(lambda: first.send(op='read', read=True)['closed'], 'replaced connection closed', timeout=15)
        newer.finish()
        first.finish()
        lab.wait_ping('b', 'c')


lib.main(test)
