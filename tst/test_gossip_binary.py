"""Independent binary record writer, strict framing, and UDP/WS/WSS descriptions."""
import socket
import struct
import time

import lib
import workload


def encode_vertex(ep):
    kind = 4 if ep['proto'] == 'udp' and ':' in ep['addr'] else {'udp': 1, 'ws': 2, 'wss': 3}[ep['proto']]
    kind |= 128 if ep.get('endpoint', ep['port'] != 0) else 0
    packet = bytearray(struct.pack('<BH', kind, ep['port']))
    if ep['proto'] == 'udp':
        packet += lib.ipbytes(ep['addr'])
    else:
        for value in (ep['addr'], ep['path']):
            data = value.encode()
            packet += struct.pack('<H', len(data)) + data
    return bytes(packet)


def encode_record(owner, version, vertices, links):
    packet = bytearray(struct.pack('<BHQH', 1, owner, version, len(vertices)))
    offsets = []
    for flags, ep in vertices:
        offsets.append(len(packet))
        packet += bytes([flags]) + encode_vertex(ep)
    packet += struct.pack('<H', len(links))
    for source, index in links:
        packet += struct.pack('<QH', lib.endpoint_hash(source), index)
    packet += struct.pack('<H', 0)
    return bytes(packet), offsets


def test():
    with lib.Lab(['a', 'b'], {1: ['a', 'b']}) as lab:
        lab.wait_ping('a', 'b')
        b_socket = lab.channel_source('b', '10.1.0.2', '10.1.0.1')
        probe = workload.Probe(lab, 'a', 'b')
        host = lib.endpoint(lib.intip(1), 0)
        listeners = [lib.endpoint('192.0.2.11', 9000),
                     dict(proto='ws', addr='edge.example.invalid', port=80, path='/mesh/λ', endpoint=True),
                     dict(proto='wss', addr='secure.example.invalid', port=443, path='/' + 'x' * 300, endpoint=True),
                     lib.endpoint('2001:db8::1234', 9000)]
        sockets = [lib.socket_vertex('192.0.2.12', 40000)]
        ident = time.time_ns() + 1_000_000_000
        vertices = [(1, ep) for ep in listeners] + [(2, ep) for ep in sockets]
        links = [(b_socket, 0)]
        edges = [(ep, host) for ep in listeners] + [(host, ep) for ep in sockets] + [(b_socket, listeners[0])]
        packet, offsets = encode_record(1, ident + 1, vertices, links)

        def send(data):
            probe.send(op='inner', hex=data.hex())

        def present(edge):
            return any(e['from'] == edge[0] and e['to'] == edge[1] for e in lab.status('b')['graph'])

        def version():
            return next((r['version'] for r in lab.status('b')['records'] if r['owner'] == 1), None)

        for end in range(1, len(packet)):
            send(packet[:end])
        send(packet + b'\0')
        send(packet[:11] + b'\xff\xff' + packet[13:])
        send(packet[:-2] + b'\xff\xff')
        send(packet[:-4] + b'\xff\xff' + packet[-2:])
        send(packet[:-12] + b'\0' * 8 + packet[-4:])
        send(packet[:-12] + struct.pack('<Q', lib.endpoint_hash(listeners[0])) + packet[-4:])
        mutations = [(offsets[0], b'\x00'), (offsets[0], b'\x04'), (offsets[0] + 1, b'\x00'), (offsets[1] + 1, b'\xff'),
                     (offsets[1] + 4, b'\xff\xff'),
                     (offsets[1] + 6 + len(listeners[1]['addr']), b'\xff\xff')]
        for offset, value in mutations:
            bad = bytearray(packet)
            bad[offset:offset + len(value)] = value
            send(bad)
        # A WS address consumes the remaining bytes, leaving no path length.
        send(struct.pack('<BHQHBBHH', 1, 1, ident + 1, 1, 1, 2, 80, 4) + b'host')
        # The first vertex consumes the bytes reserved for a second one.
        send(struct.pack('<BHQHBBHH', 1, 1, ident + 1, 2, 1, 2, 80, 8) + b'hostname' + b'\0\0')
        # IPv6 TCP socket addresses are independent vertices, including their port.
        source = struct.pack('<BBH', 2, 6, 49152) + socket.inet_pton(socket.AF_INET6, '::ffff:192.0.2.40')
        for end in range(len(source)):
            send(struct.pack('<BHQH', 1, 1, ident + 1, 1) + source[:end])

        marker = lib.endpoint('192.0.2.20', 8000)
        probe.send(op='graph', body=lib.record(1, ident, [(marker, True, False)]))
        lab.wait(lambda: present((marker, host)), 'valid gossip after malformed frames processed')
        assert version() == ident, 'malformed gossip changed the record version'
        assert not any(present(edge) for edge in edges), 'malformed gossip partially changed the graph'

        assert not any(str(lib.endpoint_hash(ep)) in lab.status('b')['addresses'] for ep in listeners + sockets)
        send(packet)
        lab.wait(lambda: all(present(edge) for edge in edges), 'independent binary writer accepted')
        assert not present((marker, host)), 'a replaced record kept an omitted vertex'
        newer = lib.record(1, ident + 2, [(ep, True, False) for ep in listeners] + [(ep, False, True) for ep in sockets],
                           [(b_socket, listeners[0])])
        probe.send(op='graph', body=newer)
        lab.wait(lambda: version() == ident + 2, 'Go writer record accepted')
        assert all(present(edge) for edge in edges), 'Go writer lost UDP, WS or WSS endpoints'
        withdrawn, _ = encode_record(1, ident + 3, [], [])
        send(withdrawn)
        lab.wait(lambda: not any(present(edge) for edge in edges), 'binary withdrawal applied')
        assert not any(str(lib.endpoint_hash(ep)) in lab.status('b')['addresses'] for ep in listeners + sockets)
        probe.finish()


lib.main(test)
