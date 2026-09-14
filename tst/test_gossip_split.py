"""Separate vertex/edge packets tolerate loss and reordering without queued edges."""
import ipaddress
import json
import struct
import time

import lib
import workload


def decode_vertices(data):
    vertices = []
    count, = struct.unpack_from('<H', data, 1)
    offset = 3
    for _ in range(count):
        flag, port = struct.unpack_from('<BH', data, offset)
        kind = flag & 127
        offset += 3
        if kind in (1, 4, 5, 6):
            size = 4 if kind in (1, 5) else 16
            address = ipaddress.ip_address(data[offset:offset+size])
            if address.version == 6 and address.ipv4_mapped:
                address = address.ipv4_mapped
            offset += size
            ep = dict(proto='tcp' if kind in (5, 6) else 'udp', addr=str(address), port=port, endpoint=bool(flag & 128))
        else:
            assert kind in (2, 3)
            values = []
            for _ in range(2):
                size, = struct.unpack_from('<H', data, offset)
                offset += 2
                values.append(data[offset:offset+size].decode())
                offset += size
            ep = dict(proto='ws' if kind == 2 else 'wss', port=port, addr=values[0], path=values[1])
        vertices.append(lib.endpoint_hash(ep))
    assert offset == len(data)
    return vertices


def test():
    with lib.Lab(['a', 'r'], {1: ['a', 'r']}) as lab:
        lab.wait_ping('a', 'r')
        probe = workload.Probe(lab, 'r', 'a')
        ident = time.time_ns()
        x, y, z = [lib.endpoint(f'192.0.2.{i}', 9000) for i in range(1, 4)]
        xy, yz = lib.edge(x, y, ident), lib.edge(y, z, ident)
        marker = lib.edge(lib.endpoint('192.0.2.10'), lib.endpoint('192.0.2.11'), ident)

        def present(edge):
            return any(e['from'] == edge['from'] and e['to'] == edge['to'] and e['id'] == edge['id']
                       for e in lab.status('a')['graph'])

        def known(vertex):
            return str(lib.endpoint_hash(vertex)) in lab.status('a')['addresses']

        def edges(records):
            probe.send(op='edges', body=lib.wire_ad(dict(edges=records))['edges'])

        def barrier():
            marker['id'] += 1
            probe.send(op='ad', body=dict(edges=[marker]))
            lab.wait(lambda: present(marker), 'earlier packets processed')

        edges([xy])
        barrier()
        assert not present(xy) and not known(x) and not known(y)
        probe.send(op='vertices', body=[x])
        lab.wait(lambda: known(x), 'standalone source vertex accepted')
        edges([xy])
        barrier()
        assert not present(xy), 'edge with unknown destination accepted'

        held = lab.intercept('r', 'a', 'hold', kind=6)
        probe.send(op='vertices', body=[y])
        lab.wait(lambda: held['hits'] == 1, 'vertex packet held')
        edges([xy])
        barrier()
        assert not present(xy)
        lab.clear(held)
        lab.release(held)
        lab.wait(lambda: known(y), 'delayed vertex accepted')
        assert not present(xy), 'an earlier edge was queued instead of discarded'
        edges([xy])
        lab.wait(lambda: present(xy), 'same edge version accepted after vertices arrive')

        dropped = lab.intercept('r', 'a', 'drop', kind=6)
        probe.send(op='vertices', body=[z])
        lab.wait(lambda: dropped['hits'] == 1, 'vertex packet lost')
        edges([yz])
        barrier()
        assert not known(z) and not present(yz)
        lab.clear(dropped)
        probe.send(op='vertices', body=[z])
        lab.wait(lambda: known(z), 'next vertex publication repairs loss')
        assert not present(yz)
        edges([yz])
        lab.wait(lambda: present(yz), 'next edge publication repairs loss')

        # Enough unique descriptions to need multiple vertex packets. A common
        # destination must still appear only once in an entire publication.
        vertices = [lib.endpoint('192.0.2.100', 10000+i) for i in range(160)]
        vertices += [dict(proto=p, addr=p+'.invalid', port=443, path='/'+'x'*300, endpoint=True) for p in ('ws', 'wss')]
        updates = [lib.edge(v, x, ident) for v in vertices]
        probe.send(op='ad', body=dict(edges=updates))
        lab.wait(lambda: all(present(edge) for edge in updates), 'large graph installed')
        captured = lab.intercept('a', 'r', 'copy', target_port=7000, count=-1)
        cursor = 0
        rounds = []
        current = None

        def received_rounds():
            nonlocal cursor, current
            packets = list(captured['held'])
            while cursor < len(packets):
                packet = packets[cursor][1]
                cursor += 1
                raw = packet[(packet[0] & 15)*4+8:]
                if raw[0] not in (4, 6):
                    continue
                probe.proc.stdin.write(json.dumps(dict(op='open', hex=raw.hex())).encode()+b'\n')
                report = probe.read()
                assert report['opened']
                inner = bytes.fromhex(report['hex'])
                assert len(inner) <= 1000
                if raw[0] == 6:
                    assert inner[0] == 5
                    if current is None or current['edges']:
                        if current is not None:
                            rounds.append(current)
                        current = dict(vertices=[], edges=[], vertex_packets=0, bytes=0)
                    current['vertices'] += decode_vertices(inner)
                    current['vertex_packets'] += 1
                else:
                    assert inner[0] == 2
                    count, = struct.unpack_from('<H', inner, 1)
                    assert len(inner) == 3+count*25, 'edge packet contains extra descriptions'
                    if current is None:
                        continue
                    current['edges'] += [struct.unpack_from('<QQ', inner, 3+i*25) for i in range(count)]
                current['bytes'] += len(raw)
            return len(rounds) >= 3

        lab.wait(received_rounds, 'three independent periodic publications', timeout=12)
        lab.clear(captured)
        expected = {(lib.endpoint_hash(e['from']), lib.endpoint_hash(e['to'])) for e in updates}
        for publication in rounds[1:3]:
            assert publication['vertex_packets'] >= 2
            assert len(publication['vertices']) == len(set(publication['vertices'])), 'repeated vertex description'
            assert len(publication['edges']) == len(set(publication['edges'])), 'repeated edge'
            assert expected <= set(publication['edges'])
            assert all(v in publication['vertices'] for edge in publication['edges'] for v in edge)
        print('split gossip:', {k: len(v) if isinstance(v, list) else v for k, v in rounds[1].items()})
        probe.finish()


lib.main(test)
