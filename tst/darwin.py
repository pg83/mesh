"""Native macOS utun: real ICMP over encrypted UDP to an independent echo peer."""
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.request

binary = Path('.build/bin/mesh').resolve()
probe = Path('.build/bin/mesh-probe').resolve()

def run(*args):
    return subprocess.check_output([str(a) for a in args], text=True)

with tempfile.TemporaryDirectory(prefix='mesh-darwin-') as directory:
    root = Path(directory)
    a, b = [json.loads(run(binary, 'keygen')) for _ in range(2)]
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
        sock.connect(('192.0.2.1', 9))
        host = sock.getsockname()[0]
    registry = [dict(name='mac', index=1, pub=a['pub'], intip='10.77.0.1', endpoint=[]),
                dict(name='echo', index=2, pub=b['pub'], intip='10.77.0.2',
                     endpoint=[dict(proto='udp', addr=host, port=17002)])]
    peer_cfg = dict(index=2, key=b['key'], registry=registry,
                    endpoint=[dict(proto='udp', addr=host, port=17002)])
    (root/'peer.json').write_text(json.dumps(peer_cfg))
    (root/'node.json').write_text(json.dumps(dict(index=1, subnet='10.77.0.0/24', mtu=1380,
        control='127.0.0.1:18058', registry=registry, endpoint=[dict(proto='udp', addr=host, port=17001)])))
    (root/'key').write_text(a['key'])
    peer_log = open(root/'peer.log', 'w+')
    peer_process = subprocess.Popen([probe, 'echo', root/'peer.json', f'{host}:17001', '1'], stdout=peer_log, stderr=peer_log)
    try:
        # A second run verifies that utun and its route disappear on exit.
        for attempt in range(2):
            node_log = open(root/f'node-{attempt}.log', 'w+')
            node = subprocess.Popen([binary, 'run', '-c', root/'node.json', '-key-file', root/'key'], stdout=node_log, stderr=node_log)
            try:
                deadline = time.monotonic()+20
                while time.monotonic()<deadline:
                    assert node.poll() is None, 'node exited'
                    try:
                        with urllib.request.urlopen('http://127.0.0.1:18058/status', timeout=1) as r:
                            status=json.load(r)
                        if status['routes'].get('10.77.0.2:0'):
                            break
                    except OSError:
                        pass
                    time.sleep(.1)
                else:
                    raise AssertionError('no route to echo peer')
                route = run('/sbin/route', '-n', 'get', '10.77.0.2')
                assert 'utun' in route, route
                for size in [56, 1200]:
                    result=run('/sbin/ping', '-n', '-c', '4', '-s', str(size), '-W', '1000', '10.77.0.2')
                    assert ' 0.0% packet loss' in result, result
                print(f'Darwin TUN round trip {attempt+1}: 8 ICMP packets, both sizes passed')
            finally:
                node.terminate()
                code=node.wait(timeout=5)
                node_log.seek(0)
                print(node_log.read())
                node_log.close()
                assert code==0, code
    finally:
        peer_process.terminate()
        peer_process.wait(timeout=5)
        peer_log.seek(0)
        print(peer_log.read())
        peer_log.close()
