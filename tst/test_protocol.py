"""Authenticated malformed packets and whole graph record replacement."""
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
            probe.stdin.write(json.dumps(dict(op=op, **fields)).encode() + b'\n')
            probe.stdin.flush()
            assert response()['sent']
        def inner(data):
            send('inner', hex=data.hex())
        def data(hops, cursor=0, payload=b''):
            return bytes([0, (len(hops) - 1) << 4 | cursor, *hops]) + payload
        a = lib.socket_vertex('10.1.0.1', 40000)
        a_counter = lib.vertex_counter(ready['source'])
        for packet in [b'\x03', b'\0', b'\0\0', b'\0\xf0' + b'\0' * 15,
                       data([2], cursor=1), data([3]), data([2, 0]), data([0]), data([3, 2]),
                       b'\x01', b'\x01\x01\0' + b'\0' * 8 + b'\xff\xff']:
            inner(packet)
        send('short-transport')
        send('short-tag')
        # Each incoming UDP channel checks the transport type and sender.
        send('raw', hex=(b'\x03' + b'\0' * 7 + b'\x01' + b'\0' * 40).hex())
        send('raw', hex=(b'\0' * 8 + b'\x03' + b'\0' * 40).hex())
        ident = time.time_ns() + 1_000_000_000
        mesh_a = lib.endpoint(lib.intip(1), 0)
        a_listener = lib.endpoint('10.1.0.1')
        # The listener is registry endpoint 1 of node a, so its id is fixed by the registry.
        body = lib.record(1, ident, [(a, False, True, a_counter), (a_listener, True, False, 1)],
                          [(lab.source_id('b', '10.1.0.2', '10.1.0.1'), a_listener)])
        send('graph', body=body)
        lab.wait_route('b', 'a', ['a'])
        send('graph', body=body)
        # A malformed record is rejected whole and leaves the current version in place.
        listener_id = lib.vertex_id(1, 1)
        for change in [dict(version=0), dict(vertices=[dict(lib.endpoint('0.0.0.0'), id=lib.vertex_id(1, 5), ingress=True, egress=False)]),
                       dict(links=[{'from': 0, 'to': listener_id}]),
                       dict(links=[{'from': listener_id, 'to': listener_id}]),
                       dict(links=[{'from': lib.vertex_id(2, 1025), 'to': lib.vertex_id(1, 7)}]),
                       dict(vertices=body['vertices'] + [dict(mesh_a, id=lib.vertex_id(1, 0), ingress=True, egress=True)]),
                       dict(vertices=body['vertices'] + [dict(a, id=lib.vertex_id(1, 1), ingress=False, egress=True)])]:
            send('graph', body=dict(body, version=ident + 1) | change)
        # A newer identical record keeps the route.
        send('graph', body=dict(body, version=ident + 2))
        lab.wait(lambda: any(r['owner'] == 1 and r['version'] == ident + 2 for r in lab.status('b')['records']), 'identical newer record applied')
        assert lab.route('b', 'a') == ['a']
        assert not any(e['from'] == mesh_a and e['to'] == mesh_a for e in lab.status('b')['graph'])
        # Datagrams to a link-local address are not from any advertised listener.
        lab.run('b', ['ip', 'addr', 'add', '169.254.1.2/16', 'dev', 's1'])
        lab.run('a', ['ip', 'addr', 'add', '169.254.1.1/16', 'dev', 's1'])
        with lab.lock:
            lab.ports[(1, socket.inet_aton('169.254.1.2'))] = lab.ports[(1, socket.inet_aton('10.1.0.2'))]
        lab.run('a', [sys.executable, '-c',
                     "import socket; s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.bind(('169.254.1.1', 0)); s.sendto(bytes([3, 2, 0]) + b'\\0' * 40, ('169.254.1.2', 7000))"])
        for ip in (b'bad', b'\x65' + b'\0' * 19, b'\x44' + b'\0' * 19,
                   b'\x4f' + b'\0' * 19, b'\x45' + b'\0' * 19):
            inner(data([2], payload=ip))
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
        inner(data([2, 3], payload=ipv4_udp(other, wrong)))
        inner(data([2], payload=ipv4_udp(lib.intip(2), good)))
        lab.wait(lambda: good.hex() in log.read_text().splitlines(), 'valid authenticated UDP reaches the server')
        assert wrong.hex() not in log.read_text().splitlines(), 'mesh delivered a packet for another route destination'
        inner(data([2, 2], payload=ipv4_udp(lib.intip(2), b'local-edge-outside-graph')))
        inner(data([2], payload=ipv4_udp(other, b'opaque-destination')))
        lab.wait(lambda: b'opaque-destination'.hex() in log.read_text().splitlines(), 'payload destination is independent of route destination')
        assert b'local-edge-outside-graph'.hex() not in log.read_text().splitlines(), 'a route revisiting the node delivered data'
        lab.wait_ping('b', 'c')
        lab.wait_ping('c', 'b')
        probe.stdin.close()
        assert probe.wait(timeout=10) == 0
        lab.wait_links('b', ['c'])
        attempt = lab.intercept('b', 'a', 'copy', kind=1)
        lab.wait(lambda: attempt['hits'] == 1, 'gossip after local link timeout')


lib.main(test)
