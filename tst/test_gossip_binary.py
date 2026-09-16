"""Independent binary record writer, strict framing, and UDP/WS/WSS descriptions."""
import socket
import random
import struct
import time

import lib
import work_load as workload


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


def counter(value):
    return struct.pack('<I', value)[:3]


def encode_record(owner, version, vertices, links, observed=()):
    """vertices: (counter, flags, vertex); links: (source id, counter); observed: (source id, seen)."""
    packet = bytearray(struct.pack('<BHQBH', 1, owner, version, 0, len(vertices)))
    offsets = []
    for number, flags, ep in vertices:
        offsets.append(len(packet))
        packet += counter(number) + bytes([flags]) + encode_vertex(ep)
    packet += struct.pack('<H', len(links))
    for source, number in links:
        packet += struct.pack('<I', source) + counter(number)
    packet += struct.pack('<H', len(observed))
    for source, seen in observed:
        packet += struct.pack('<I', source) + encode_vertex(seen)
    return bytes(packet), offsets


def test():
    with lib.Lab(['a', 'b'], {1: ['a', 'b']}) as lab:
        lab.wait_ping('a', 'b')
        b_socket = lab.channel_source('b', '10.1.0.2', '10.1.0.1')
        b_source = lab.source_id('b', '10.1.0.2', '10.1.0.1')
        probe = workload.Probe(lab, 'a', 'b')
        host = lib.endpoint(lib.intip(1), 0)
        listeners = [lib.endpoint('192.0.2.11', 9000),
                     dict(proto='ws', addr='edge.example.invalid', port=80, path='/mesh/λ', endpoint=True),
                     dict(proto='wss', addr='secure.example.invalid', port=443, path='/' + 'x' * 300, endpoint=True),
                     lib.endpoint('2001:db8::1234', 9000)]
        sockets = [lib.socket_vertex('192.0.2.12', 40000), lib.socket_vertex('2001:db8::1234', 9000)]
        ident = time.time_ns() + 1_000_000_000
        vertices = [(201 + i, 1, ep) for i, ep in enumerate(listeners)] + [(205 + i, 2, ep) for i, ep in enumerate(sockets)]
        links = [(b_source, 201)]
        observed = [(b_source, lib.endpoint('203.0.113.5', 40123))]
        edges = [(ep, host) for ep in listeners] + [(host, ep) for ep in sockets] + [(b_socket, listeners[0])]
        packet, offsets = encode_record(1, ident + 1, vertices, links, observed)
        tail = len(packet) - 2 - 4 - 7
        ids = [lib.vertex_id(1, number) for number, _, _ in vertices]

        def send(data, compress=True):
            probe.send(op='inner', hex=data.hex(), compress=compress)

        def present(edge):
            return any(e['from'] == edge[0] and e['to'] == edge[1] for e in lab.status('b')['graph'])

        def version():
            return next((r['version'] for r in lab.status('b')['records'] if r['owner'] == 1), None)

        for end in range(1, len(packet)):
            send(packet[:end])
        # Records travel as zstd frames: an uncompressed record and a broken frame are invalid.
        send(packet, compress=False)
        send(b'\x28\xb5\x2f\xfd' + b'\0' * 20, compress=False)
        send(packet + b'\0')
        send(packet[:11] + b'\xff\xff' + packet[13:])
        send(packet[:tail] + b'\xff\xff')
        # Link targets must be vertices of the record; sources must not be mesh IPs or the target itself.
        send(packet[:tail - 3] + b'\xff\xff\xff' + packet[tail:])
        send(packet[:tail - 7] + struct.pack('<I', lib.vertex_id(2, 0)) + packet[tail - 3:])
        send(packet[:tail - 7] + struct.pack('<I', ids[0]) + packet[tail - 3:])
        # Observations must name a linked source and describe a UDP address with a port.
        send(packet[:tail + 2] + struct.pack('<I', ids[0]) + packet[tail + 6:])
        send(packet[:tail + 6] + encode_vertex(listeners[1]))
        send(packet[:tail + 6] + encode_vertex(lib.endpoint('203.0.113.5', 0)))
        # Record flags beyond exit, vertex counters that repeat or are zero, and bad
        # vertex flags or descriptions are invalid.
        mutations = [(11, b'\x02'), (offsets[0] + 3, b'\x00'), (offsets[0] + 3, b'\x04'), (offsets[0] + 4, b'\x00'), (offsets[1] + 4, b'\xff'),
                     (offsets[1] + 7, b'\xff\xff'),
                     (offsets[1] + 9 + len(listeners[1]['addr']), b'\xff\xff'),
                     (offsets[0], b'\x00\x00\x00'), (offsets[1], counter(201)), (offsets[0] + 5, b'\x00\x00\x00\x00\x00\x00')]
        for offset, value in mutations:
            bad = bytearray(packet)
            bad[offset:offset + len(value)] = value
            send(bad)
        head = struct.pack('<BHQBH', 1, 1, ident + 1, 0, 1) + counter(7)
        # A WS address consumes the remaining bytes, leaving no path length.
        send(head + struct.pack('<BBHH', 1, 2, 80, 4) + b'host')
        # The first vertex consumes the bytes reserved for a second one.
        send(struct.pack('<BHQBH', 1, 1, ident + 1, 0, 2) + counter(7) + struct.pack('<BBHH', 1, 2, 80, 8) + b'hostname' + b'\0\0')
        # IPv6 TCP socket addresses are independent vertices, including their port.
        source = struct.pack('<BBH', 2, 6, 49152) + socket.inet_pton(socket.AF_INET6, '::ffff:192.0.2.40')
        for end in range(len(source)):
            send(head + source[:end])

        # Version bundles: a bad frame, no chunks, a short chunk, a zero owner, short
        # entries, a zero entry owner and trailing bytes are invalid as a whole.
        chunk = lambda owner, entries, tail=b'': bytes([1, owner]) + struct.pack('<Q', ident + 10) + bytes([len(entries)]) + b''.join(bytes([o]) + struct.pack('<Q', v) for o, v in entries) + tail
        bad_bundles = [b'\x28\xb5\x2f\xfd' + b'\0' * 8, b'', bytes([1, 2]), chunk(0, [(1, 1)]), bytes([1, 2]) + struct.pack('<Q', 7) + bytes([3]) + b'\0' * 9,
                       chunk(2, [(0, 1)]), chunk(2, [(1, 1)], b'\0')]
        for bundle in bad_bundles:
            send(b'\x03' + bundle, compress=bundle != bad_bundles[0])
        invalid = lambda: next(float(l.split()[1]) for l in lab.http('b', '/metrics')[2].decode().splitlines() if l.startswith('mesh_vectors_invalid_total'))
        lab.wait(lambda: invalid() == len(bad_bundles), 'malformed bundles counted')
        # A vector arrives in two chunks of one version and merges; with 255
        # entries of random versions it does not compress into one datagram, so
        # b's own bundle takes several packets.
        assert lab.status('b')['bundles'] == 1
        big = [(o, random.getrandbits(64)) for o in range(1, 256)]
        send(b'\x03' + chunk(1, big[:128]))
        send(b'\x03' + chunk(1, big[128:]))
        lab.wait(lambda: len(next((v['records'] for v in lab.status('b')['vectors'] if v['owner'] == 1), [])) == 255, 'big vector stored')
        assert lab.status('b')['bundles'] >= 2, lab.status('b')['bundles']
        assert invalid() == len(bad_bundles)

        marker = lib.endpoint('192.0.2.20', 8000)
        probe.send(op='graph', body=lib.record(1, ident, [(marker, True, False)]))
        lab.wait(lambda: present((marker, host)), 'valid gossip after malformed frames processed')
        assert version() == ident, 'malformed gossip changed the record version'
        assert not any(present(edge) for edge in edges), 'malformed gossip partially changed the graph'

        # The dial channels towards the withdrawn listeners go with the next snapshot.
        lab.wait(lambda: not any(str(ident) in lab.status('b')['addresses'] for ident in ids), 'withdrawn vertices forgotten')
        send(packet)
        lab.wait(lambda: all(present(edge) for edge in edges), 'independent binary writer accepted')
        assert not present((marker, host)), 'a replaced record kept an omitted vertex'
        record = next(r for r in lab.status('b')['records'] if r['owner'] == 1)
        assert [v['id'] for v in record['vertices']] == ids
        assert record['observed'] == [dict(zip(['from', 'seen'], [b_source, observed[0][1]]))]
        # A link from a vertex that its owner does not publish is not an edge, and its observation is ignored.
        ghost = lib.vertex_id(2, 2100)
        newer = lib.record(1, ident + 2, [(ep, True, False, 201 + i) for i, ep in enumerate(listeners)] + [(ep, False, True, 205 + i) for i, ep in enumerate(sockets)],
                           [(b_source, listeners[0]), (ghost, listeners[0])], observed + [(ghost, lib.endpoint('203.0.113.6', 40124))])
        probe.send(op='graph', body=newer)
        lab.wait(lambda: version() == ident + 2, 'Go writer record accepted')
        assert all(present(edge) for edge in edges), 'Go writer lost UDP, WS or WSS endpoints'
        assert [e['from'] for e in lab.status('b')['graph'] if e['to'] == listeners[0]] == [b_socket]
        withdrawn, _ = encode_record(1, ident + 3, [], [])
        send(withdrawn)
        lab.wait(lambda: not any(present(edge) for edge in edges), 'binary withdrawal applied')
        lab.wait(lambda: not any(str(ident) in lab.status('b')['addresses'] for ident in ids), 'withdrawn vertices forgotten')
        probe.finish()


lib.main(test)
