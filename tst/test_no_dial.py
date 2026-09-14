"""Directed IP exclusions remove only selected dials, including learned endpoints."""
import json
import time

import lib


class Lab(lib.Lab):
    def registry(self):
        registry = super().registry()
        for peer in registry:
            peer['endpoint'] += [dict(ep, proto='ws', port=7100) for ep in peer['endpoint']]
        return registry


def rolling_restart():
    with lib.Lab(['a', 'b', 'observer'], {1: ['a', 'b', 'observer'], 2: ['a', 'b']}) as lab:
        lab.wait_ping('a', 'b')
        lab.wait_ping('observer', 'a')
        for name, other in [('a', 'b'), ('b', 'a')]:
            lab.configs[name] = dict(no_dial=[
                {'from': lab.nodes[name].addresses[1], 'to': lab.nodes[other].addresses[1]}])
            lab.stop_node(name)
            lab.start_node(name)
        lab.wait_ping('observer', 'a')
        lab.wait_ping('observer', 'b')

        def clean():
            for name in lab.nodes:
                for edge in lab.status(name)['graph']:
                    if (edge['from']['addr'], edge['to']['addr']) in [
                            ('10.1.0.1', '10.1.0.2'), ('10.1.0.2', '10.1.0.1')]:
                        return False
            return True

        lab.wait(clean, 'old excluded links withdrawn after rolling restart', timeout=12)
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')


def test():
    lab = Lab(['a', 'b', 'laptop'], {1: ['a', 'b', 'laptop'], 2: ['a', 'b'], 3: ['a', 'b']},
              ipv6=[3], statics=['a', 'b'])
    for name, other in [('a', 'b'), ('b', 'a')]:
        source, target = lab.nodes[name].index, lab.nodes[other].index
        lab.configs[name] = dict(no_dial=[
            {'from': f'::ffff:10.1.0.{source}', 'to': f'10.1.0.{target}'},
            {'from': f'2001:db8:3::{source}', 'to': f'2001:db8:3::{target}'},
        ])
    with lab:
        for a, b in [('a', 'b'), ('b', 'a'), ('a', 'laptop'), ('laptop', 'b')]:
            lab.wait_ping(a, b)
        time.sleep(2)
        for a, b in [('a', 'b'), ('b', 'a')]:
            assert lab.traffic(a, b, 1) == 0
            assert lab.traffic(a, b, 3) == 0
            assert lab.traffic(a, b, 2) > 0
        assert lab.traffic('a', 'laptop', 1) > 0
        assert lab.traffic('laptop', 'b', 1) > 0
        code, _, body = lab.http('a', '/config?node=laptop')
        assert code == 200
        exported = json.loads(body)
        assert 'no_dial' not in exported and 'key' not in exported

        udp_back = [lab.intercept('a', 'b', 'observe', seg=s, target_port=7000, count=-1) for s in [1, 3]]
        lab.stop_node('b')
        lab.configs['b']['no_dial'] = []
        lab.start_node('b')
        lab.wait(lambda: all(lab.traffic(a, b, seg) > 0
                            for a, b in [('b', 'a')] for seg in [1, 3]),
                 'the independently enabled reverse direction sends packets')
        time.sleep(2)
        assert all(rule['hits'] == 0 for rule in udp_back), 'receiving UDP created a return channel'
        lab.wait_ping('a', 'b')
        status = lab.status('a')
        for connection in status['channels']:
            vertices = [status['addresses'][str(connection[k])] for k in ['from', 'to']]
            for source, target in [vertices, vertices[::-1]]:
                if connection['outgoing'] and source['proto'] == 'udp':
                    assert (source['addr'], target['addr']) not in [
                        ('10.1.0.1', '10.1.0.2'), ('2001:db8:3::1', '2001:db8:3::2')]

        lab.stop_node('a')
        cfg = json.loads((lab.dir / 'a.json').read_text())
        for pair in [{'from': '10.1.0.1'}, {'to': '10.1.0.2'},
                     {'from': '10.1.0.0/24', 'to': '10.1.0.2'}]:
            cfg['no_dial'] = [pair]
            path = lab.dir / 'invalid.json'
            path.write_text(json.dumps(cfg))
            result = lab.run('a', [lib.MESH, 'run', '-c', path], check=False)
            assert result.returncode != 0
            assert 'no_dial requires' in result.stderr or 'ParseAddr' in result.stderr, result.stderr
    rolling_restart()


lib.main(test)
