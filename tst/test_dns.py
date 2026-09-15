"""Nodes answer the mesh zone on the subnet's service address and their own address; mesh dns serves it from the control API."""
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


def ask(lab, name, server, question, qtype=1, port=53, timeout=3):
    code = ('import socket, sys; s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.settimeout(%r); '
            's.sendto(bytes.fromhex(sys.argv[1]), (%r, %d)); sys.stdout.write(s.recv(512).hex())' % (timeout, server, port))
    out = lab.run(name, ['python3', '-c', code, query(question, qtype).hex()]).stdout
    return parse(bytes.fromhex(out))


def name_of(rdata):
    labels, offset = [], 0
    while rdata[offset]:
        labels.append(rdata[offset + 1:offset + 1 + rdata[offset]].decode())
        offset += 1 + rdata[offset]
    return '.'.join(labels) + '.'


def test():
    lab = lib.Lab(['a', 'b', 'c'], {1: ['a', 'b', 'c']}, statics=['c'])
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
        assert ask(lab, 'a', service, '9.0.77.10.in-addr.arpa', qtype=12)[0] == 3
        assert ask(lab, 'a', service, 'example.com')[0] == 5
        assert ask(lab, 'a', service, '1.0.0.10.in-addr.arpa', qtype=12)[0] == 5
        # A peer asks the node's own address across the mesh, on the flag-configured port.
        assert ask(lab, 'a', lib.intip(2), 'c.mesh', port=5353) == (0, [(1, socket.inet_aton(lib.intip(3)))])
        # The standalone command serves the same zone from the control API on a plain socket.
        lab.spawn('a', [lib.MESH, 'dns', '-control', 'localhost:8058', '-listen', '127.0.0.1:5355'], 'dns')
        lab.wait(lambda: ask(lab, 'a', '127.0.0.1', 'c.mesh', port=5355, timeout=1) == (0, [(1, socket.inet_aton(lib.intip(3)))]),
                 'standalone dns serves the zone', timeout=15)
        assert ask(lab, 'a', '127.0.0.1', 'example.org', port=5355)[0] == 5


lib.main(test)
