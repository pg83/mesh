"""Independent binary gossip writer, strict framing, and UDP/WS/WSS descriptions."""
import socket
import struct
import time

import lib
import workload


def encode(endpoints, edges):
    packet = bytearray(struct.pack('<BHH', 2, len(edges), len(endpoints)))
    for edge in edges:
        packet += struct.pack('<QQQB', lib.endpoint_hash(edge['from']),
                              lib.endpoint_hash(edge['to']), edge['id'], edge['alive'])
    offsets = []
    for ep in endpoints:
        offsets.append(len(packet))
        kind = 4 if ep['proto'] == 'udp' and ':' in ep['addr'] else {'udp': 1, 'ws': 2, 'wss': 3}[ep['proto']]
        packet += struct.pack('<BH', kind, ep['port'])
        if ep['proto'] == 'udp':
            packet += lib.ipbytes(ep['addr'])
        else:
            for value in (ep['addr'], ep['path']):
                data = value.encode()
                packet += struct.pack('<H', len(data)) + data
    return bytes(packet), offsets


def test():
    with lib.Lab(['a', 'b'], {1: ['a', 'b']}) as lab:
        lab.wait_ping('a', 'b')
        probe = workload.Probe(lab, 'a', 'b')
        endpoints = [lib.endpoint('192.0.2.10', 0), lib.endpoint('192.0.2.11', 9000),
                     dict(proto='ws', addr='edge.example.invalid', port=80, path='/mesh/λ'),
                     dict(proto='wss', addr='secure.example.invalid', port=443, path='/' + 'x' * 300),
                     lib.endpoint('2001:db8::1234', 9000)]
        ident = time.time_ns()
        edges = [lib.edge(a, b, ident) for a, b in zip(endpoints, endpoints[1:])]
        packet, offsets = encode(endpoints, edges)

        def send(data):
            probe.send(op='inner', hex=data.hex())

        def present(edge):
            return any(e['from'] == edge['from'] and e['to'] == edge['to'] and e['id'] == edge['id']
                       for e in lab.status('b')['graph'])

        for end in range(len(packet)):
            send(packet[:end])
        send(packet + b'\0')
        mutations = [(1, b'\xff\xff'), (3, b'\xff\xff'), (5 + 24, b'\x02'),
                     (offsets[0], b'\x00'), (offsets[2], b'\x05'),
                     (offsets[2] + 3, b'\xff\xff'),
                     (offsets[2] + 5 + len(endpoints[2]['addr']), b'\xff\xff')]
        for offset, value in mutations:
            bad = bytearray(packet)
            bad[offset:offset + len(value)] = value
            send(bad)
        # A WS address consumes the remaining bytes, leaving no path length.
        send(struct.pack('<BHHBHH', 2, 0, 1, 2, 80, 4) + b'host')
        # The first endpoint consumes the bytes reserved for a second one.
        send(struct.pack('<BHHBHH', 2, 0, 2, 2, 80, 8) + b'hostname' + b'\0\0')

        marker = lib.edge(lib.endpoint('192.0.2.20', 8000), lib.endpoint('192.0.2.21', 8000), ident)
        probe.send(op='ad', body=dict(edges=[marker]))
        lab.wait(lambda: present(marker), 'valid gossip after malformed frames processed')
        assert not any(present(edge) for edge in edges), 'malformed gossip partially changed the graph'

        send(packet)
        lab.wait(lambda: all(present(edge) for edge in edges), 'independent binary writer accepted')
        newer = [dict(edge, id=ident + 1) for edge in edges]
        probe.send(op='ad', body=dict(edges=newer))
        lab.wait(lambda: all(present(edge) for edge in newer), 'Go writer preserves UDP, WS and WSS endpoints')
        withdrawn = [dict(edge, id=ident + 2, alive=False) for edge in edges]
        send(encode(endpoints, withdrawn)[0])
        lab.wait(lambda: not any(present(edge) for edge in newer), 'binary withdrawals applied')
        probe.finish()


lib.main(test)
