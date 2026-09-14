"""Authenticated malformed packets and independent directed graph record merging."""
import json
import os
import select
import socket
import struct
import subprocess
import sys
import time
import lib
import workload


def test():
    with lib.Lab(['a', 'b', 'c'], {1: ['a', 'b', 'c']}) as lab:
        lab.wait_ping('b', 'c')
        lab.stop_node('a')
        probe = lab.spawn('a', [os.environ['MESH_TEST_PROBE'], lab.dir / 'a.json', '10.1.0.2:7000', '2'],
                          'protocol-probe', stdin=subprocess.PIPE, stdout=subprocess.PIPE)
        def response():
            assert select.select([probe.stdout], [], [], 10)[0], 'probe timed out'
            return json.loads(probe.stdout.readline())
        ready = response()
        assert ready['ready']
        def send(op, **fields):
            if op == 'ad':
                ad = lib.wire_ad(fields['body'])
                send('vertices', body=ad['vertices'])
                send('edges', body=ad['edges'])
                return
            probe.stdin.write(json.dumps(dict(op=op, **fields)).encode() + b'\n')
            probe.stdin.flush()
            assert response()['sent']
        def inner(data):
            send('inner', hex=data.hex())
        def data(path, cursor=0, payload=b''):
            return bytes([1, len(path), cursor]) + b''.join(struct.pack('<Q', lib.endpoint_hash(ep))
                     for edge in path for ep in edge) + payload
        a = ready['source']
        b, c = [lib.endpoint(f'10.1.0.{i}') for i in (2, 3)]
        for packet in [b'', b'\xff', b'\x01', b'\x01\0\0', b'\x01\x11\0',
                       data([(a,b)], cursor=1), data([(a,c)]), data([(a,b),(b,lib.endpoint('10.1.0.99'))]),
                       b'\x02', b'\x02{']:
            inner(packet)
        send('short-transport')
        send('short-tag')
        # Each incoming UDP channel checks the transport type and sender.
        send('raw', hex=(b'\xff' + b'\x01\0' + b'\0' * 40).hex())
        send('raw', hex=(b'\x03' + b'\x03\0' + b'\0' * 40).hex())
        ident = time.time_ns() + 1_000_000_000
        mesh_a = lib.endpoint(lib.intip(1), 0)
        a_listener = lib.endpoint('10.1.0.1')
        records = [lib.edge(mesh_a, a, ident), lib.edge(a_listener, mesh_a, ident), lib.edge(lab.channel_source('b', '10.1.0.2', '10.1.0.1'), a_listener, ident)]
        body = dict(edges=records)
        send('ad', body=body)
        lab.wait_route('b', 'a', ['a'])
        send('ad', body=body)
        # Reject malformed graph entries without discarding independent valid pairs.
        for change in [dict(id=0), {'from':lib.endpoint('0.0.0.0')}, {'to':b}]:
            send('ad', body=dict(edges=[dict(records[2], **dict(id=ident+1) | change)]))
        # An update to one pair does not remove another pair omitted from this batch.
        send('ad', body=dict(edges=[dict(records[0], id=ident+2)]))
        assert lab.route('b', 'a') == ['a']
        mesh_b = lib.endpoint(lib.intip(2), 0)
        full_path = [(mesh_a, a), (a, b), (b, mesh_b)]
        for ip in (b'bad', b'\x65' + b'\0' * 19, b'\x44' + b'\0' * 19,
                   b'\x4f' + b'\0' * 19, b'\x45' + b'\0' * 19):
            inner(data(full_path, cursor=1, payload=ip))
        # Route destination, not the payload address, selects the local TUN.
        other = '10.77.0.99'
        lab.run('b', ['ip', 'addr', 'add', other + '/32', 'dev', 'lo'])
        log = workload.udp_server(lab, 'b', host='0.0.0.0')
        lab.run('b', [sys.executable, '-c',
                     "import socket; socket.socket(socket.AF_INET, socket.SOCK_DGRAM).sendto(b'listener-ready', ('10.77.0.99', 9000))"])
        lab.wait(lambda: b'listener-ready'.hex() in log.read_text().splitlines(), 'listener accepts the other local IP')
        def ipv4_udp(destination, payload):
            udp = struct.pack('!HHHH', 9001, 9000, 8 + len(payload), 0) + payload
            head = bytearray(struct.pack('!BBHHHBBH4s4s', 0x45, 0, 20 + len(udp), 0, 0, 64, 17, 0,
                                         socket.inet_aton(lib.intip(1)), socket.inet_aton(destination)))
            checksum = sum(struct.unpack('!10H', head))
            while checksum >> 16:
                checksum = (checksum & 0xffff) + (checksum >> 16)
            struct.pack_into('!H', head, 10, ~checksum & 0xffff)
            return bytes(head) + udp
        wrong = b'wrong-inner-destination'
        good = b'correct-inner-destination'
        inner(data([(mesh_a, a), (a, b), (b, lib.endpoint(lib.intip(3), 0))], cursor=1, payload=ipv4_udp(other, wrong)))
        inner(data(full_path, cursor=1, payload=ipv4_udp(lib.intip(2), good)))
        lab.wait(lambda: good.hex() in log.read_text().splitlines(), 'valid authenticated UDP reaches the server')
        assert wrong.hex() not in log.read_text().splitlines(), 'mesh delivered a packet for another route destination'
        inner(data(full_path, cursor=1, payload=ipv4_udp(other, b'opaque-destination')))
        lab.wait(lambda: b'opaque-destination'.hex() in log.read_text().splitlines(), 'payload destination is independent of route destination')
        lab.wait_ping('b', 'c')
        lab.wait_ping('c', 'b')
        probe.stdin.close()
        assert probe.wait(timeout=10) == 0
        lab.wait_links('b', ['c'])
        attempt = lab.intercept('b', 'a', 'copy', kind=4)
        lab.wait(lambda: attempt['hits'] == 1, 'gossip after local link timeout')


lib.main(test)
