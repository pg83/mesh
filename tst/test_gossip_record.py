"""Records are independent: links wait for their source owner, periodic gossip resends every record."""
import json
import struct
import time

import lib
import workload


def test():
    with lib.Lab(['a', 'r', 'b'], {1: ['a', 'r', 'b']}) as lab:
        lab.wait_nodes('a', ['a', 'r', 'b'])
        lab.stop_node('b')
        probe = workload.Probe(lab, 'r', 'a')
        ident = time.time_ns() + 1_000_000_000
        host_r, host_b = [lib.endpoint(lib.intip(i), 0) for i in (2, 3)]
        x = lib.endpoint('10.1.0.2')
        y = lib.socket_vertex('10.1.0.3', 40000)
        marker = lib.endpoint('192.0.2.10', 9000)

        def present(src, dst):
            return any(e['from'] == src and e['to'] == dst for e in lab.status('a')['graph'])

        y_id = lib.record_id(3, 0)

        def known(ident):
            return str(ident) in lab.status('a')['addresses']

        probe.send(op='graph', body=lib.record(2, ident, [(x, True, False)], [(y_id, x)]))
        lab.wait(lambda: present(x, host_r), 'record applied')
        assert not present(y, x) and not known(y_id), 'link with an unpublished source vertex appeared'
        probe.send(op='graph', body=lib.record(3, ident, [(y, False, True)]))
        lab.wait(lambda: present(y, x) and present(host_b, y), 'link resolved when its source owner publishes the vertex')
        probe.send(op='graph', body=lib.record(3, ident + 1, []))
        lab.wait(lambda: not known(y_id), 'vertex withdrawal removes the vertex')
        assert not present(y, x), 'link survived the withdrawal of its source vertex'
        assert present(x, host_r), 'an unrelated record changed'
        probe.send(op='graph', body=lib.record(3, ident, [(y, False, True)]))
        probe.send(op='graph', body=lib.record(2, ident + 1, [(x, True, False), (marker, True, False)], [(y_id, x)]))
        lab.wait(lambda: present(marker, host_r), 'barrier record applied')
        assert not known(y_id) and not present(y, x), 'a stale record revived a withdrawn vertex'

        captured = lab.intercept('a', 'r', 'copy', target_port=7000, count=-1)
        cursor = 0
        seen = []

        def publications():
            nonlocal cursor
            packets = list(captured['held'])
            while cursor < len(packets):
                packet = packets[cursor][1]
                cursor += 1
                raw = packet[(packet[0] & 15) * 4 + 8:]
                if raw[0] & 3 != 1:
                    continue
                probe.proc.stdin.write(json.dumps(dict(op='open', hex=raw.hex())).encode() + b'\n')
                report = probe.read()
                assert report['opened']
                inner = bytes.fromhex(report['hex'])
                assert len(inner) <= 1000
                assert report['kind'] == 1
                owner, version = struct.unpack_from('<HQ', inner)
                seen.append((owner, version))
            return all(sum(1 for owner, _ in seen if owner == index) >= 3 for index in (1, 2, 3))

        lab.wait(publications, 'every known record is republished periodically', timeout=12)
        lab.clear(captured)
        assert {(2, ident + 1), (3, ident + 1)} <= set(seen), 'relayed records changed their versions'
        print('record gossip:', sorted(set(seen)))
        probe.finish()


lib.main(test)
