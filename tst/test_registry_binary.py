"""Independent registry framing, bounded random batches and invalid records."""
import struct
import json

import lib
import workload


def string(value):
    data = value.encode()
    return struct.pack('<H', len(data)) + data


def encode(records):
    data = bytearray(struct.pack('<BH', 4, len(records)))
    for p in records:
        data += struct.pack('<HQ', p['index'], p['version']) + lib.ipbytes(p['intip'])
        data += string(p['pub']) + string(p.get('name', ''))
        data += struct.pack('<H', len(p['endpoint']))
        for ep in p['endpoint']:
            kind = 4 if ep['proto'] == 'udp' and ':' in ep['addr'] else {'udp': 1, 'ws': 2, 'wss': 3}[ep['proto']]
            data += struct.pack('<BH', kind | 128, ep['port'])
            data += lib.ipbytes(ep['addr']) if ep['proto'] == 'udp' else string(ep['addr']) + string(ep['path'])
    return bytes(data)


def test():
    with lib.Lab(['a', 'b', 'c'], {1: ['a', 'b', 'c'], 2: ['a', 'b', 'c']}) as lab:
        lab.wait_ping('b', 'c')
        probe = workload.Probe(lab, 'a', 'b')

        def send(data):
            probe.send(op='inner', hex=data.hex())

        def records(name='b'):
            return {p['index']: p for p in lab.status(name)['registry']}

        peer = dict(index=40, version=9, name='new', pub=lab.nodes['a'].keys['pub'],
                    intip=lib.intip(40), endpoint=[lib.endpoint('2001:db8::40'),
                        dict(proto='ws', addr='edge.invalid', port=80, path='/mesh/λ'),
                        dict(proto='wss', addr='edge.invalid', port=443, path='/mesh')])
        peer['endpoint'] = [{k: v for k, v in ep.items() if k != 'endpoint'} for ep in peer['endpoint']]
        packet = encode([peer])
        for end in range(len(packet)):
            send(packet[:end])
        send(packet + b'\0')
        bad = bytearray(packet)
        bad[3 + 14 + 2 + len(peer['pub']) + 2 + len(peer['name']) + 2] = 0
        send(bad)
        send(b'\4\xff\xff')
        send(b'\4' + b'\0' * 1200)
        for change in (dict(index=0), dict(version=0), dict(pub='bad'),
                       dict(pub='A' * 43 + '='), dict(endpoint=[lib.endpoint('::', 0)]),
                       dict(endpoint=[dict(proto='ws', addr='edge.invalid', port=80, path='bad')])):
            send(encode([peer | change]))
        assert 40 not in records()
        send(packet)
        lab.wait(lambda: 40 in records(), 'independent registry writer accepted')
        assert records()[40] == peer

        # Equal and lower versions do not change an already known record.
        send(encode([peer | dict(name='wrong')]))
        send(encode([peer | dict(version=1, name='old')]))
        send(encode([peer | dict(version=10, name='updated')]))
        lab.wait(lambda: records()[40]['version'] == 10, 'higher version accepted')
        assert records()[40]['name'] == 'updated'

        # An otherwise valid batch may contain an unusable key or an attempted
        # change of the receiver's own key/IP; independent records still apply.
        own = dict(lab.registry()[1], version=100, intip=lib.intip(99))
        send(encode([own, peer | dict(index=41, pub='bad'), peer | dict(index=42)]))
        lab.wait(lambda: 42 in records(), 'valid record survives invalid neighbor')
        assert records()[2]['intip'] == lib.intip(2) and 41 not in records()

        # Populate beyond one packet. Every relay send is bounded, while random
        # independent batches eventually cover the whole local database.
        oversized = lab.intercept('b', 'c', 'observe', kind=5, min_size=1201, count=-1)
        for index in range(50, 66):
            send(encode([peer | dict(index=index, intip=lib.intip(index),
                                    name='entry-' + str(index) + '-' * 120, endpoint=[])]))
        lab.wait(lambda: all(i in records('c') for i in range(50, 66)),
                 'random batches cover all records through relay', timeout=120)
        assert oversized['hits'] == 0
        probe.finish()

        # A single record must fit as a unit; invalid local configurations
        # fail before opening sockets or TUN instead of disappearing on wire.
        for size, message in ((1200, 'does not fit in one packet'), (65536, 'registry string too long')):
            cfg = json.loads((lab.dir / 'a.json').read_text())
            cfg['registry'][0]['name'] = 'x' * size
            path = lab.dir / 'oversized.json'
            path.write_text(json.dumps(cfg))
            result = lab.run('a', [lib.MESH, 'run', '-c', path], check=False)
            assert result.returncode != 0 and message in result.stderr, result


lib.main(test)
