"""Raw DNS queries shared by the DNS end-to-end topologies."""
import struct


def query(name, qtype=1, ident=7):
    labels = b''.join(bytes([len(l)]) + l.encode() for l in name.strip('.').split('.'))
    return struct.pack('>HHHHHH', ident, 0x0100, 1, 0, 0, 0) + labels + b'\0' + struct.pack('>HH', qtype, 1)


def parse(reply, ttl=60):
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
        kind, klass, answer_ttl, size = struct.unpack_from('>HHIH', reply, offset + 2)
        assert klass == 1 and answer_ttl == ttl, (klass, answer_ttl, ttl)
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


def ask(lab, name, server, question, qtype=1, port=53, timeout=3, ttl=60):
    reply = send(lab, name, server, query(question, qtype), port, timeout)
    return parse(reply, ttl) if reply else None


def name_of(rdata):
    labels, offset = [], 0
    while rdata[offset]:
        labels.append(rdata[offset + 1:offset + 1 + rdata[offset]].decode())
        offset += 1 + rdata[offset]
    return '.'.join(labels) + '.'
