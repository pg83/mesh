"""A test-only authenticated peer exercises malformed inner packets and gossip."""

import json
import os
import select
import struct
import subprocess
import time
from pathlib import Path

import lib


def test():
    lab = lib.Lab(['a', 'b', 'c'], {1: ['a', 'b', 'c']})
    with lab:
        lab.wait_links('b', ['a', 'c'])
        lab.wait_links('c', ['a', 'b'])
        lab.stop_node('a')
        probe = lab.spawn('a', [os.environ['MESH_TEST_PROBE'], lab.dir / 'a.json', '10.1.0.2:7000', '2'],
                          'protocol-probe', stdin=subprocess.PIPE, stdout=subprocess.PIPE)
        def response():
            assert select.select([probe.stdout], [], [], 10)[0], 'probe timed out'
            return json.loads(probe.stdout.readline())
        assert response()['ready']
        def send(op, **fields):
            probe.stdin.write(json.dumps(dict(op=op, **fields)).encode() + b'\n')
            probe.stdin.flush()
            assert response()['sent']
        def inner(data):
            send('inner', hex=data.hex())
        def data(path, cursor=0, payload=b''):
            return b'\x01\x01\x00' + bytes([len(path)]) + b''.join(struct.pack('<H', p) for p in path) + bytes([cursor]) + payload
        for packet in [b'', b'\xff', b'\x01', b'\x01\1\0\0', b'\x01\1\0\x11',
                       data([2], cursor=1), data([3]), data([2, 99]), b'\x02', b'\x02' + b'\0' * 64 + b'{']:
            inner(packet)
        send('short-transport')
        now = time.time_ns() + 1_000_000_000
        body = dict(index=1, ts=now, addrs=['invalid', '10.77.0.1:7000'], neighbors=[])
        send('ad', body=body)
        lab.wait(lambda: lab.nodes['a'].index in lab.links('b'), 'probe peer alive')
        send('ad', body=body)  # same announcement must not flood again
        send('ad', body=dict(body, index=99, ts=now + 1))
        send('ad', body=dict(body, index=3, ts=now + 2))  # signature belongs to a, not c
        # Routing requires both endpoints to advertise the link.
        send('ad', body=dict(body, ts=now + 3, neighbors=[2]))
        lab.wait(lambda: lab.status('b')['routes'].get('1') == [1], 'mutual link advertised')
        send('ad', body=body)
        # Keep the real b/c application route healthy after all malformed traffic.
        lab.wait_ping('b', 'c')
        lab.wait_ping('c', 'b')
        assert lab.status('b')['routes']['1'] == [1]
        assert '99' not in lab.status('b')['routes']
        assert 99 not in lab.status('b')['nodes']
        # Delivery with a valid route but invalid IP payload must not kill a node.
        for ip in (b'bad', b'\x65' + b'\0' * 19, b'\x44' + b'\0' * 19,
                   b'\x4f' + b'\0' * 19, b'\x45' + b'\0' * 19):
            inner(data([2], payload=ip))
        lab.wait_ping('c', 'b')
        probe.stdin.close()
        assert probe.wait(timeout=10) == 0
        lab.wait_links('b', ['c'], timeout=30)
        probe_attempt = lab.intercept('b', 'a', 'copy', kind=3, min_size=52, max_size=52)
        lab.wait(lambda: probe_attempt['hits'] == 1, 'keepalive to the expired peer')
        lab.wait_ping('b', 'c')


lib.main(test)
