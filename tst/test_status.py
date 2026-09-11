"""Complete status streams and failed observations through the real CLI."""

from concurrent.futures import ThreadPoolExecutor
import json
import socket
import subprocess
import sys
import time

import lib


def fragmented_status():
    status = dict(index=1, links=[], nodes=[1], pending=0,
                  routes={str(i): [1, 2, 3] for i in range(60000)})
    payload = json.dumps(status).encode() + b'\n'
    assert len(payload) > 1 << 20
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as server:
        server.settimeout(10)
        server.bind('\0status-fragments')
        server.listen()
        def serve():
            conn, _ = server.accept()
            with conn:
                conn.settimeout(10)
                conn.sendall(payload[:9])
                time.sleep(.1)
                conn.sendall(payload[9:])
        with ThreadPoolExecutor(max_workers=1) as pool:
            sent = pool.submit(serve)
            result = subprocess.run([lib.MESH, 'status', '-s', '@status-fragments'],
                                    capture_output=True, timeout=10)
            sent.result(timeout=10)
        assert result.returncode == 0, result.stderr
        assert result.stdout == payload, (len(result.stdout), len(payload))


def unavailable_status(lab, error_type):
    try:
        lab.links('a')
    except error_type:
        pass
    else:
        raise AssertionError('links hid a status error')
    try:
        lab.wait_links('a', [], timeout=.4)
    except AssertionError as error:
        assert error_type.__name__ in str(error), error
    else:
        raise AssertionError('failed status satisfied an empty-links assertion')


def test():
    with lib.Lab(['a', 'b'], {1: ['a', 'b']}) as lab:
        lab.wait_ping('a', 'b')
        fragmented_status()
        lab.stop_node('a')
        lab.configs['a'] = {'status': ''}
        lab.start_node('a')
        lab.wait_ping('a', 'b')
        unavailable_status(lab, OSError)

        ready = lab.dir / 'invalid-status-ready'
        code = '''
import socket
import sys
from pathlib import Path
with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as server:
    server.bind('\\0mesh')
    server.listen()
    Path(sys.argv[1]).touch()
    while True:
        conn, _ = server.accept()
        with conn:
            conn.sendall(b'{')
'''
        server = lab.spawn('a', [sys.executable, '-c', code, ready], 'invalid-status')
        lab.wait(ready.exists, 'invalid status server ready')
        unavailable_status(lab, json.JSONDecodeError)
        server.terminate()
        server.wait(timeout=5)

        lab.stop_node('a')
        lab.block('a', 'b')
        lab.configs['a'] = {}
        lab.start_node('a')
        lab.wait_links('a', [])
        lab.unblock('a', 'b')
        lab.wait_ping('a', 'b')


lib.main(test)
