"""A delayed WS accept rejects a removed listener; a wait without a state change expires."""
import os
import time

import lib
import ws


def test():
    lab = ws.Lab(statics=['b'])
    lab.configs['a'] = dict(endpoint=[])
    with lab:
        lab.wait_ping('a', 'b')
        if not os.environ.get('MESH_CHAOS'):
            return
        lab.stop_node('a')
        for remove in [True, False]:
            lab.stop_node('b')
            lab.node_env['b'] = {'MESH_CHAOS': 'channel accept pause:1'}
            lab.start_node('b')
            lab.wait_links('b', [])
            def listening():
                return any(v['proto'] == 'ws' and v['addr'] == '10.1.0.2'
                           for r in lab.status('b')['records'] if r['owner'] == 2
                           for v in r['vertices'])
            lab.wait(listening, 'WS listener published')
            log = lab.dir / 'b.log'
            offset = log.stat().st_size
            def messages():
                return log.read_text()[offset:]
            probe = ws.Probe(lab)
            probe.send(op='graph', body=lib.record(1, time.time_ns(), []))
            lab.wait(lambda: 'at="channel accept pause"' in messages(), 'accept waits for an interface change')
            assert not lab.channels('b'), 'channel installed before the wait ended'
            if remove:
                lab.run('b', ['ip', 'addr', 'del', '10.1.0.2/24', 'dev', 's1'])
                lab.wait(lambda: not listening(), 'listener removed before the delayed channel arrives')
                lab.wait(lambda: lab.tcp_connections('b') == 0, 'obsolete channel closes its socket')
                assert not lab.channels('b'), 'obsolete channel installed'
                lab.run('b', ['ip', 'addr', 'add', '10.1.0.2/24', 'dev', 's1'])
                assert probe.send(op='read', read=True)['closed'], 'obsolete channel stayed open'
            else:
                lab.wait(lambda: 'chaos gave up waiting' in messages(), 'wait expires without an interface change', timeout=40)
                assert not probe.send(op='graph', body=lib.record(1, time.time_ns(), []), read=True)['closed']
                lab.wait(lambda: len(lab.channels('b')) == 2, 'valid delayed channel installed after the timeout')
            probe.finish()
        lab.stop_node('b')
        lab.node_env['b'] = {'MESH_CHAOS': None}
        lab.start_node('b')
        lab.start_node('a')
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')


lib.main(test)
