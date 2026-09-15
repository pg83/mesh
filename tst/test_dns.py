"""Nodes answer the mesh zone on the subnet's service address and their own address; mesh dns serves it from the control API."""
import json
import socket
import struct
import lib


def query(name, qtype=1, ident=7):
    labels = b''.join(bytes([len(l)]) + l.encode() for l in name.strip('.').split('.'))
    return struct.pack('>HHHHHH', ident, 0x0100, 1, 0, 0, 0) + labels + b'\0' + struct.pack('>HH', qtype, 1)


def parse(reply):
    """(rcode, [(type, rdata)]) of a reply to a one-question query with compressed answer names."""
    ident, flags, qd, an, ns, ar = struct.unpack_from('>HHHHHH', reply)
    assert flags & 0x8000 and qd == 1
    offset = 12
    while reply[offset]:
        offset += 1 + reply[offset]
    offset += 5
    answers = []
    for _ in range(an):
        assert reply[offset] == 0xc0
        kind, klass, ttl, size = struct.unpack_from('>HHIH', reply, offset + 2)
        assert klass == 1 and ttl == 60
        answers.append((kind, reply[offset + 12:offset + 12 + size]))
        offset += 12 + size
    return flags & 15, answers


def send(lab, name, server, packet, port=53, timeout=3):
    """The reply to a raw packet, or None when the server stays silent."""
    code = ('import socket, sys; s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.settimeout(%r); '
            's.sendto(bytes.fromhex(sys.argv[1]), (%r, %d))\n'
            'try: sys.stdout.write(s.recv(512).hex())\n'
            'except socket.timeout: pass' % (timeout, server, port))
    out = lab.run(name, ['python3', '-c', code, packet.hex()]).stdout
    return bytes.fromhex(out) if out else None


def ask(lab, name, server, question, qtype=1, port=53, timeout=3):
    return parse(send(lab, name, server, query(question, qtype), port, timeout))


def name_of(rdata):
    labels, offset = [], 0
    while rdata[offset]:
        labels.append(rdata[offset + 1:offset + 1 + rdata[offset]].decode())
        offset += 1 + rdata[offset]
    return '.'.join(labels) + '.'


class Lab(lib.Lab):
    def registry(self):
        registry = super().registry()
        # A name that is not a single label has no place in the zone.
        registry.append(dict(name='bad.name', index=9, pub=registry[0]['pub'], intip=lib.intip(9), endpoint=[]))
        return registry


def test():
    lab = Lab(['a', 'b', 'c'], {1: ['a', 'b', 'c']}, statics=['c'])
    lab.configs['a'] = dict(dns=True)
    lab.run_args['b'] = ['-dns', '-dns-port', '5353']
    with lab:
        lab.wait_ping('a', 'b')
        lab.wait_ping('a', 'c')
        service = lib.SUBNET.split('/')[0]
        # The local system asks the subnet's service address, answered by its own node.
        assert ask(lab, 'a', service, 'b.mesh') == (0, [(1, socket.inet_aton(lib.intip(2)))])
        assert ask(lab, 'a', service, 'B.MESH.') == (0, [(1, socket.inet_aton(lib.intip(2)))])
        rcode, answers = ask(lab, 'a', service, '2.0.77.10.in-addr.arpa', qtype=12)
        assert rcode == 0 and [(kind, name_of(rdata)) for kind, rdata in answers] == [(12, 'b.mesh.')]
        assert ask(lab, 'a', service, 'b.mesh', qtype=28) == (0, []), 'AAAA of a known name is an empty answer'
        assert ask(lab, 'a', service, 'nobody.mesh')[0] == 3
        assert ask(lab, 'a', service, 'bad.name.mesh')[0] == 3
        assert ask(lab, 'a', service, '9.0.77.10.in-addr.arpa', qtype=12)[0] == 3
        assert ask(lab, 'a', service, 'example.com')[0] == 5
        assert ask(lab, 'a', service, '1.0.0.10.in-addr.arpa', qtype=12)[0] == 5
        # Malformed queries are dropped: short, a response, two questions, a truncated
        # name, a label over 63 bytes, a name too long; a wrong class and a bad octet
        # in a reverse name are refused; a PTR name asked for another type is empty.
        q = query('b.mesh')
        for bad in [q[:11], bytes([0, 7, 0x81]) + q[3:], q[:4] + b'\0\x02' + q[6:], q[:16], q[:12] + b'\x40' + b'x' * 64 + q[13:],
                    q[:12] + b'\x01x' * 40 + q[12:]]:
            assert send(lab, 'a', service, bad, timeout=1) is None, bad.hex()
        assert parse(send(lab, 'a', service, q[:-2] + b'\0\x03'))[0] == 5
        assert ask(lab, 'a', service, '256.0.77.10.in-addr.arpa', qtype=12)[0] == 5
        assert ask(lab, 'a', service, '01.0.77.10.in-addr.arpa', qtype=12)[0] == 5
        assert ask(lab, 'a', service, '2.0.77.10.in-addr.arpa', qtype=1) == (0, [])
        # A peer asks the node's own address across the mesh, on the flag-configured port.
        assert ask(lab, 'a', lib.intip(2), 'c.mesh', port=5353) == (0, [(1, socket.inet_aton(lib.intip(3)))])
        # The standalone command serves the same zone from the control API on a plain socket.
        lab.spawn('a', [lib.MESH, 'dns', '-control', 'localhost:8058', '-listen', '127.0.0.1:5355'], 'dns')
        lab.wait(lambda: ask(lab, 'a', '127.0.0.1', 'c.mesh', port=5355, timeout=1) == (0, [(1, socket.inet_aton(lib.intip(3)))]),
                 'standalone dns serves the zone', timeout=15)
        assert ask(lab, 'a', '127.0.0.1', 'example.org', port=5355)[0] == 5
        # A DNS port outside 1..65535 stops the node at startup.
        lab.stop_node('c')
        config = json.loads((lab.dir / 'c.json').read_text())
        config.update(dns=True, dns_port=70000)
        (lab.dir / 'bad.json').write_text(json.dumps(config))
        result = lab.run('c', [lib.MESH, 'run', '-c', lab.dir / 'bad.json'], check=False, timeout=10)
        assert result.returncode != 0 and 'bad dns port 70000' in result.stderr, result


lib.main(test)
