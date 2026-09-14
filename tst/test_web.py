"""Real control/web servers, keyless bootstrap, graph changes and upstream recovery."""
import json
import os
import socket
import subprocess
import sys

import lib


def test():
    with lib.Lab(['a', 'mini', 'work'], {1: ['a', 'mini', 'work']}, statics=['a']) as lab:
        for name in ['mini', 'work']:
            lab.wait_ping('a', name)
        web = lab.spawn('a', [lib.MESH, 'web', '-control', 'localhost:8058',
                             '-listen', '127.0.0.1:8059'], 'web')

        def get(path, status=200, port=8059):
            code, headers, body = lab.http('a', path, port)
            assert code == status, (path, code, body[:200])
            assert all(n.keys['key'].encode() not in body for n in lab.nodes.values())
            return headers, body

        lab.wait(lambda: lab.run('a', [lib.MESH, 'status'], check=False).returncode == 0, 'control ready')
        for _ in range(100):
            try:
                get('/')
                break
            except OSError:
                import time
                time.sleep(.05)
        else:
            raise AssertionError('web did not listen')
        for path in ['/', '/config', '/app.js', '/style.css', '/cytoscape.js']:
            assert get(path)[1]
        get('/missing', 404)
        get('/api/config', 404)
        get('/api/config?node=unknown', 404)
        get('/api/config?node=a', 400)
        headers, body = get('/api/topology')
        assert headers['Cache-Control'] == 'no-store'
        topo = json.loads(body)
        assert topo['index'] == 1 and len(topo['peers']) == 3
        ids = {v['id'] for v in topo['vertices']}
        assert all(isinstance(i, str) for i in ids)
        assert any(int(i) > 2**53 for i in ids)
        assert all(e[k] in ids for e in topo['edges'] for k in ['source', 'target'])
        assert {v['owner'] for v in topo['vertices']} >= {1, 2, 3}
        assert topo['routes'][lib.intip(3) + ':0']
        code = 'import socket; s=socket.socket(); s.settimeout(2); assert s.connect_ex(("10.1.0.1",8058)) != 0'
        lab.run('mini', [sys.executable, '-c', code])

        # Bootstrap from the downloaded file with only the private key supplied separately.
        headers, body = get('/api/config?node=mini')
        cfg = json.loads(body)
        assert 'key' not in cfg and 'tun' not in cfg
        assert cfg['index'] == 2 and cfg['subnet'] == lib.SUBNET
        assert cfg['endpoint'] == []
        assert cfg['registry'] == [p for p in lab.registry() if p['endpoint'] or p['index'] == 2]
        assert cfg.get('registry_version', 1) == 1
        assert 'attachment' in headers['Content-Disposition']
        assert json.loads(get('/api/config?node=2')[1]) == cfg
        lab.stop_node('mini')
        key = lab.dir / 'mini.key'
        key.write_text(lab.nodes['mini'].keys['key'])
        lab.configs['mini'] = dict(cfg, key='', endpoint=[lib.endpoint('0.0.0.0')])
        lab.run_args['mini'] = ['-key-file', str(key)]
        lab.start_node('mini')
        lab.wait_ping('mini', 'work')

        # Matrix browser check also exercises a two-hop route in the actual mesh.
        lab.block('a', 'work')
        lab.wait_route('a', 'work', ['mini', 'work'])
        topo = json.loads(get('/api/topology')[1])
        assert len(topo['routes'][lib.intip(3) + ':0']) == 6
        if python := os.environ.get('MESH_TEST_BROWSER_PYTHON'):
            lab.run('a', [python, str(lib.Path(__file__).with_name('browser.py'))],
                    timeout=90, capture_output=False)

        # Read-only export must not include host-specific binding or TLS key paths.
        lab.stop_node('a')
        reg = lab.registry()
        reg[0]['endpoint'][0].update(tls_key='/secret/tls-private-key', bind_addr='0.0.0.0')
        lab.configs['a'] = {'registry': reg}
        get('/api/topology', 502)
        lab.start_node('a')
        lab.wait_ping('a', 'mini')
        for path in ['/api/topology', '/api/config?node=work']:
            body = get(path)[1]
            assert b'tls_key' not in body and b'bind_addr' not in body and b'/secret/' not in body
        web.terminate()
        assert web.wait(timeout=10) == 0


lib.main(test)
