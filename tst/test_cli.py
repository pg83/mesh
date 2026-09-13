"""Configuration failures, localhost HTTP control, and malformed public UDP input."""

import copy
import json
import os
import signal
import subprocess
import sys

import lib


def test():
    def cli(*args, expected=None):
        result = subprocess.run([lib.MESH, *args], capture_output=True, text=True, timeout=10)
        assert result.returncode != 0
        if expected:
            assert expected in result.stderr, result.stderr
    cli(expected='Usage:')
    cli('unknown', expected='Usage:')
    cli('run', '-c', '/no/such/mesh-config')
    cli('status', '-control', '127.0.0.1:65534')
    cli('status', '-control', '192.0.2.1:8058', expected='loopback')
    cli('status', '-control', 'bad', expected='missing port')
    cli('status', '-control', 'localhost:0', expected='bad control port')
    cli('status', '-control', 'localhost:no', expected='invalid syntax')
    with lib.Lab(['a', 'b'], {1: ['a', 'b']}) as lab:
        lab.wait_ping('a', 'b')
        config = json.loads((lab.dir / 'a.json').read_text())
        def bad(change, expected):
            cfg = copy.deepcopy(config)
            cfg['endpoint'] = [dict(proto='udp', addr='0.0.0.0', port=7900)]
            cfg['registry'][0]['endpoint'] = []
            cfg['tun'] = 'invalid-test'
            cfg['control'] = ''
            change(cfg)
            path = lab.dir / 'bad.json'
            path.write_text(json.dumps(cfg))
            result = lab.run('a', [lib.MESH, 'run', '-c', path], check=False, timeout=10)
            assert result.returncode != 0
            assert expected in result.stderr, result.stderr
            lab.run('a', ['ip', 'link', 'del', 'invalid-test'], check=False)
        bad(lambda c: c.update(index=99), 'not in registry')
        bad(lambda c: c.update(index=0), 'not in registry')
        bad(lambda c: c['registry'][0].update(index=0), 'index 0 is reserved')
        bad(lambda c: c['registry'][1].update(index=0), 'index 0 is reserved')
        bad(lambda c: c['registry'][0].pop('index'), 'index 0 is reserved')
        bad(lambda c: c.update(key='not base64!'), 'base64')
        bad(lambda c: c.update(key='YQ=='), 'bad key length')
        bad(lambda c: c.update(key=lab.nodes['b'].keys['key']), 'private key does not match')
        bad(lambda c: c['registry'].append(c['registry'][0]), 'duplicate index')
        bad(lambda c: c['registry'][0].update(intip='bad'), 'bad intip')
        bad(lambda c: c['endpoint'][0].update(proto='bad'), 'bad endpoint proto')
        bad(lambda c: c['endpoint'][0].update(port=0), 'bad endpoint port')
        bad(lambda c: c['endpoint'][0].update(port=65536), 'bad endpoint port')
        bad(lambda c: c['endpoint'][0].update(bind_port=-1), 'bad endpoint port')
        bad(lambda c: c['endpoint'][0].update(bind_port=65536), 'bad endpoint port')
        bad(lambda c: c['endpoint'][0].update(proto='ws', bind_proto='udp'), 'bad bind_proto')
        bad(lambda c: c['endpoint'][0].update(proto='ws', path='relative'), 'bad websocket path')
        bad(lambda c: c['endpoint'][0].update(proto='ws', path='/%zz'), 'bad websocket path')
        bad(lambda c: c['endpoint'].extend([
            dict(proto='ws', addr='0.0.0.0', port=7901),
            dict(proto='wss', addr='0.0.0.0', port=7901),
        ]), 'conflicting listener protocols')
        bad(lambda c: c['endpoint'][0].update(proto='wss', tls_cert='/absent'), 'no such file')
        bad(lambda c: c['endpoint'][0].update(proto='wss'), 'TLS listener needs tls_cert and tls_key')
        bad(lambda c: c['registry'][1]['endpoint'][0].update(proto='ws', path='/' + 'x' * 65536),
            'endpoint string too long')
        bad(lambda c: c['endpoint'].append(dict(proto='udp', addr='203.0.113.1', port=17001,
                                              bind_addr='10.1.0.1', bind_port=7900)), 'ambiguous endpoint binding')
        bad(lambda c: c['endpoint'].extend([
            dict(proto='udp', addr='203.0.113.1', port=17001, bind_addr='10.1.0.1', bind_port=7901),
            dict(proto='udp', addr='203.0.113.1', port=17001, bind_addr='10.1.0.1', bind_port=7902),
        ]), 'ambiguous public endpoint')
        bad(lambda c: c.update(subnet='bad'), 'CIDR')
        bad(lambda c: c['endpoint'][0].update(port=7000), 'address already in use')
        bad(lambda c: c.update(control='0.0.0.0:8058'), 'loopback')
        path = lab.dir / 'bad.json'
        path.write_text('{')
        result = lab.run('a', [lib.MESH, 'run', '-c', path], check=False)
        assert result.returncode != 0 and 'JSON' in result.stderr

        lab.stop_node('a')
        lab.run('a', ['ip', 'link', 'del', 'mesh0'])
        lab.configs['a'] = {'control': 'localhost:8058', 'tun': 'mesh-test', 'mtu': 1280}
        lab.start_node('a')
        lab.wait_links('a', ['b'])
        result = lab.run('a', [lib.MESH, 'status', '-control', lib.CONTROL])
        assert json.loads(result.stdout)['index'] == 1
        lab.nodes['a'].proc.send_signal(signal.SIGINT)
        assert lab.nodes['a'].proc.wait(timeout=10) == 0
        lab.nodes['a'].proc = None
        lab.run('a', ['ip', 'link', 'del', 'mesh-test'])
        lab.configs['a'] = {}
        lab.start_node('a')
        lab.wait_ping('a', 'b')

        # The kernel accepts these UDP datagrams; mesh must ignore them.
        packets = ['', 'ff', '01', '0100000001', '02', '020000000100000002',
                   '03', '03000000010000000000000000']
        code = ('import socket; s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); '
                f'[s.sendto(bytes.fromhex(p),("10.1.0.2",7000)) for p in {packets!r}]')
        lab.run('a', [sys.executable, '-c', code])
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')


lib.main(test)
