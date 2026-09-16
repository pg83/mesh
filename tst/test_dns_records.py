"""DNS pools follow routes through relays, shuffle addresses, and expire when control is unavailable."""
import json
import socket
import sys

import lib
from dns import ask


def test():
    lab = lib.Lab(['a', 'r', 'b', 'c'], {1: ['a', 'r'], 2: ['r', 'b', 'c']}, statics=['r', 'b', 'c'])
    lab.configs['a'] = dict(dns=True, dns_records={
        '*.LAB.': ['a', 'b', 'C.', 'b'],
        'special.lab': ['a'],
        '*.deep.lab': ['b'],
        'leaf.blocked.lab': ['c'],
        'backends': ['b', 'c'],
        'empty.lab': ['missing'],
    })
    with lab:
        lab.wait_route('a', 'b', ['r', 'b'])
        lab.wait_route('a', 'c', ['r', 'c'])
        service = lib.SUBNET.split('/')[0]
        lab.spawn('a', [lib.MESH, 'dns', '-listen', '127.0.0.1:5355'], 'dns')
        servers = [(service, 53), ('127.0.0.1', 5355)]

        def resolve(server, name='grafana.lab.mesh', qtype=1):
            address, port = server
            return ask(lab, 'a', address, name, qtype=qtype, port=port, ttl=5, timeout=1)

        def expected(*names):
            return {socket.inet_aton(lib.intip(lab.nodes[name].index)) for name in names}

        def matches(server, names, name='grafana.lab.mesh', rcode=0):
            reply = resolve(server, name)
            if reply is None:
                return False
            code, answers = reply
            return code == rcode and len(answers) == len(names) and {data for kind, data in answers if kind == 1} == expected(*names)

        def wait_answers(names, name='grafana.lab.mesh', rcode=0):
            lab.wait(lambda: all(matches(s, names, name, rcode) for s in servers),
                     f'{name}: {names}, rcode={rcode}', timeout=15)

        wait_answers(['a', 'b', 'c'])
        status = lab.status('a')
        assert status['dns_records']['*.lab'] == ['a', 'b', 'c', 'b']
        exported = json.loads(lab.http('a', '/config?node=a')[2])
        assert exported['dns'] and exported['dns_records'] == status['dns_records']
        assert 'key' not in exported

        for server in servers:
            orders = set()
            for _ in range(24):
                rcode, answers = resolve(server)
                assert rcode == 0 and len(answers) == 3
                assert {data for kind, data in answers if kind == 1} == expected('a', 'b', 'c')
                orders.add(tuple(data for _, data in answers))
            assert len(orders) > 1, orders
            assert matches(server, ['a', 'b', 'c'], 'GRAFANA.LAB.MESH.')
            assert matches(server, ['a', 'b', 'c'], 'api.grafana.lab.mesh')
            assert matches(server, ['a', 'b', 'c'], '*.lab.mesh')
            assert matches(server, ['a'], 'special.lab.mesh')
            assert matches(server, ['b'], 'x.deep.lab.mesh')
            assert matches(server, ['c'], 'leaf.blocked.lab.mesh')
            assert resolve(server, 'grafana.lab.mesh', qtype=28) == (0, [])
            assert {data for _, data in resolve(server, qtype=255)[1]} == expected('a', 'b', 'c')
            assert resolve(server, 'lab.mesh') == (0, [])
            assert resolve(server, 'mesh') == (0, [])
            assert resolve(server, 'x.blocked.lab.mesh') == (3, [])
            assert resolve(server, 'x.special.lab.mesh') == (3, [])
            assert resolve(server, 'x.*.lab.mesh') == (3, [])
            assert resolve(server, 'empty.lab.mesh') == (2, [])
            assert resolve(server, 'empty.lab.mesh', qtype=28) == (0, [])
            assert resolve(server, 'grafana.lab') == (5, [])

        # b still sends to r: it stays alive, but nobody can send to it.
        lab.block('r', 'b', both=False)
        lab.block('c', 'b', both=False)
        lab.wait_route('a', 'b', None)
        assert lab.nodes['b'].index in lab.status('a')['alive']
        wait_answers(['a', 'c'])
        wait_answers([], 'x.deep.lab.mesh', rcode=2)
        for _ in range(3):
            assert all(matches(s, ['a', 'c']) for s in servers)

        lab.unblock('r', 'b', both=False)
        lab.unblock('c', 'b', both=False)
        lab.wait_route('a', 'b', ['r', 'b'])
        wait_answers(['a', 'b', 'c'])

        lab.stop_node('b')
        lab.stop_node('c')
        wait_answers(['a'])
        wait_answers([], 'backends.mesh', rcode=2)
        lab.start_node('b')
        lab.start_node('c')
        wait_answers(['a', 'b', 'c'])

        # Even a valid status body cannot refresh DNS when its HTTP status is 503.
        (lab.dir / 'status.json').write_bytes(lab.http('a', '/status')[2])
        lab.stop_node('a')
        script = '''from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
import sys
payload = Path(sys.argv[1]).read_bytes()
class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(503)
        self.send_header('Content-Length', str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)
    def log_message(self, *args):
        pass
HTTPServer(('127.0.0.1', 8058), Handler).serve_forever()
'''
        fake = lab.spawn('a', [sys.executable, '-c', script, lab.dir / 'status.json'], 'unavailable-control')
        def ready():
            try:
                return lab.http('a', '/status')[0] == 503
            except OSError:
                return False
        lab.wait(ready, '503 control listening')
        lab.wait(lambda: resolve(servers[1]) == (2, []), 'standalone DNS expires its stale snapshot', timeout=10)
        assert ask(lab, 'a', '127.0.0.1', '1.0.77.10.in-addr.arpa', qtype=12, port=5355) == (2, [])
        fake.terminate()
        fake.wait(timeout=5)
        lab.configs['a']['dns'] = False
        lab.start_node('a')
        lab.wait(lambda: matches(servers[1], ['a', 'b', 'c']),
                 'standalone DNS recovers with embedded DNS disabled', timeout=15)


lib.main(test)
