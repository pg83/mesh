"""Configuration failures, real status sockets, and malformed public UDP input."""

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
    cli('status', '-s', '@absent')
    with lib.Lab(['a', 'b'], {1: ['a', 'b']}) as lab:
        lab.wait_ping('a', 'b')
        config = json.loads((lab.dir / 'a.json').read_text())
        def bad(change, expected):
            cfg = copy.deepcopy(config)
            cfg['port'] = 7900
            cfg['tun'] = 'invalid-test'
            cfg['status'] = ''
            change(cfg)
            path = lab.dir / 'bad.json'
            path.write_text(json.dumps(cfg))
            result = lab.run('a', [lib.MESH, 'run', '-c', path], check=False, timeout=10)
            assert result.returncode != 0
            assert expected in result.stderr, result.stderr
        bad(lambda c: c.update(index=99), 'not in registry')
        bad(lambda c: c.update(key='not base64!'), 'base64')
        bad(lambda c: c.update(key='YQ=='), 'bad key length')
        bad(lambda c: c.update(key=lab.nodes['b'].keys['key']), 'private key does not match')
        bad(lambda c: c['registry'][0].update(sig=lab.nodes['b'].keys['sig']), 'signing key does not match')
        bad(lambda c: c['registry'].append(c['registry'][0]), 'duplicate index')
        bad(lambda c: c['registry'][0].update(intip='bad'), 'bad intip')
        bad(lambda c: c['registry'][0].update(static=['bad']), 'missing port')
        bad(lambda c: c.update(subnet='bad'), 'CIDR')
        bad(lambda c: c.update(port=7000), 'address already in use')
        bad(lambda c: c.update(status='/' + 'x' * 110), 'status socket path')
        path = lab.dir / 'bad.json'
        path.write_text('{')
        result = lab.run('a', [lib.MESH, 'run', '-c', path], check=False)
        assert result.returncode != 0 and 'JSON' in result.stderr

        # Exercise the filesystem socket as well as the usual abstract one.
        lab.stop_node('a')
        sock = lab.dir / 'status.sock'
        # Run from a short relative path; checkout/build TMPDIR can be arbitrarily long.
        lab.configs['a'] = {'status': 'status.sock', 'tun': 'mesh-test', 'mtu': 1280}
        saved = os.getcwd()
        try:
            os.chdir(lab.dir)
            lab.start_node('a')
        finally:
            os.chdir(saved)
        lab.wait(lambda: sock.exists(), 'filesystem status socket')
        result = lab.run('a', [lib.MESH, 'status', '-s', 'status.sock'], cwd=lab.dir)
        assert json.loads(result.stdout)['index'] == 1
        lab.nodes['a'].proc.send_signal(signal.SIGINT)
        assert lab.nodes['a'].proc.wait(timeout=10) == 0
        lab.nodes['a'].proc = None
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
