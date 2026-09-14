"""Independent binary gossip writer, strict framing, and UDP/WS/WSS descriptions."""
import socket
import struct
import time

import lib
import workload


def encode_edges(edges):
    packet = bytearray(struct.pack('<BH', 2, len(edges)))
    for edge in edges:
        packet += struct.pack('<QQQB', lib.endpoint_hash(edge['from']),
                              lib.endpoint_hash(edge['to']), edge['id'], edge['alive'])
    return bytes(packet)


def encode_vertices(endpoints):
    packet = bytearray(struct.pack('<BH', 5, len(endpoints)))
    offsets = []
    for ep in endpoints:
        offsets.append(len(packet))
        kind = 4 if ep['proto'] == 'udp' and ':' in ep['addr'] else {'udp': 1, 'ws': 2, 'wss': 3}[ep['proto']]
        kind |= 128 if ep.get('endpoint', ep['port'] != 0) else 0
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
                     dict(proto='ws', addr='edge.example.invalid', port=80, path='/mesh/λ', endpoint=True),
                     dict(proto='wss', addr='secure.example.invalid', port=443, path='/' + 'x' * 300, endpoint=True),
                     lib.endpoint('2001:db8::1234', 9000)]
        ident = time.time_ns()
        edges = [lib.edge(a, b, ident) for a, b in zip(endpoints, endpoints[1:])]
        vertices, offsets = encode_vertices(endpoints)
        edge_packet = encode_edges(edges)

        def send(data):
            probe.send(op='inner', hex=data.hex())

        def present(edge):
            return any(e['from'] == edge['from'] and e['to'] == edge['to'] and e['id'] == edge['id']
                       for e in lab.status('b')['graph'])

        for packet in (vertices, edge_packet):
            for end in range(len(packet)):
                send(packet[:end])
            send(packet + b'\0')
            send(packet[:1] + b'\xff\xff' + packet[3:])
        bad = bytearray(edge_packet)
        bad[3 + 24] = 2
        send(bad)
        mutations = [(offsets[0], b'\x00'), (offsets[2], b'\xff'),
                     (offsets[2] + 3, b'\xff\xff'),
                     (offsets[2] + 5 + len(endpoints[2]['addr']), b'\xff\xff')]
        for offset, value in mutations:
            bad = bytearray(vertices)
            bad[offset:offset + len(value)] = value
            send(bad)
        # A WS address consumes the remaining bytes, leaving no path length.
        send(struct.pack('<BHBHH', 5, 1, 2, 80, 4) + b'host')
        # The first endpoint consumes the bytes reserved for a second one.
        send(struct.pack('<BHBHH', 5, 2, 2, 80, 8) + b'hostname' + b'\0\0')
        # IPv6 TCP socket addresses are independent vertices, including their port.
        source = struct.pack('<BH', 6, 49152) + socket.inet_pton(socket.AF_INET6, '::ffff:192.0.2.40')
        for end in range(len(source)):
            send(struct.pack('<BH', 5, 1) + source[:end])

        marker = lib.edge(lib.endpoint('192.0.2.20', 8000), lib.endpoint('192.0.2.21', 8000), ident)
        probe.send(op='ad', body=dict(edges=[marker]))
        lab.wait(lambda: present(marker), 'valid gossip after malformed frames processed')
        assert not any(present(edge) for edge in edges), 'malformed gossip partially changed the graph'

        assert not any(str(lib.endpoint_hash(ep)) in lab.status('b')['addresses'] for ep in endpoints)
        send(vertices)
        send(edge_packet)
        lab.wait(lambda: all(present(edge) for edge in edges), 'independent binary writer accepted')
        newer = [dict(edge, id=ident + 1) for edge in edges]
        probe.send(op='ad', body=dict(edges=newer))
        lab.wait(lambda: all(present(edge) for edge in newer), 'Go writer preserves UDP, WS and WSS endpoints')
        withdrawn = [dict(edge, id=ident + 2, alive=False) for edge in edges]
        send(encode_edges(withdrawn))
        lab.wait(lambda: not any(present(edge) for edge in newer), 'binary withdrawals applied')
        probe.finish()


lib.main(test)
