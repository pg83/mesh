"""Full directed routes carry IPv6 and opaque payloads through a real relay."""
import json
import socket
import struct
import sys
import time
import lib
import workload


def encode(hops, payload, cursor=0):
    return bytes([(len(hops) - 1) << 4 | cursor, *hops]) + payload


def checksum(data):
    data += b'\0' * (len(data) % 2)
    value = sum(struct.unpack('!'+'H'*(len(data)//2), data))
    while value >> 16:
        value = (value & 65535) + (value >> 16)
    return (~value & 65535) or 65535


def test():
    with lib.Lab(['a', 'r', 'b'], {1: ['a', 'r'], 2: ['r', 'b']}) as lab:
        lab.wait_ping('a', 'b')
        route = lab.full_route('a', 'b')
        assert len(route) == 6
        assert route[0]['from'] == lib.endpoint(lib.intip(1), 0)
        assert route[-1]['to'] == lib.endpoint(lib.intip(3), 0)
        assert all(a['to'] == b['from'] for a, b in zip(route, route[1:]))
        origin = workload.Probe(lab, 'a', 'r')
        hops = [lab.nodes['r'].index, lab.nodes['b'].index]
        destination = 'fd77::3'
        lab.run('b', ['ip', '-6', 'addr', 'add', destination+'/128', 'dev', 'lo', 'nodad'])
        log = lab.dir/'ipv6-receive.log'
        program = "import socket; s=socket.socket(socket.AF_INET6,socket.SOCK_DGRAM); s.bind(('fd77::3',9010)); print('ready',flush=True);\nwhile True: print(s.recv(65535).hex(),flush=True)"
        lab.spawn('b', [sys.executable, '-u', '-c', program], 'ipv6-receive')
        lab.wait(lambda: log.exists() and 'ready' in log.read_text(), 'IPv6 destination ready')
        body = b'IPv6 payload addressed independently of the mesh route'
        src, dst = socket.inet_pton(socket.AF_INET6, 'fd77::1'), socket.inet_pton(socket.AF_INET6, destination)
        udp = bytearray(struct.pack('!HHHH', 9011, 9010, len(body)+8, 0) + body)
        pseudo = src+dst+struct.pack('!I3xB', len(udp), 17)
        struct.pack_into('!H', udp, 6, checksum(pseudo+udp))
        packet = struct.pack('!IHBB', 6 << 28, len(udp), 17, 64)+src+dst+udp
        origin.send(op='inner', hex=(b'\0' + encode(hops, packet)).hex())
        lab.wait(lambda: body.hex() in log.read_text(), 'IPv6 traversed relay and reached TUN by full route')

        # Authenticate/decode as the destination without interpreting its payload.
        receiver = workload.Probe(lab, 'b', 'r', seg=2)
        copied = lab.intercept('r', 'b', 'copy', target_port=7000, kind=0, count=-1)
        offset = 0
        for payload in [b'\0opaque\xff', b'\x60not-an-IP-header', b'', bytes(range(256))*4]:
            origin.send(op='inner', hex=(b'\0' + encode(hops, payload)).hex())
            def arrived():
                nonlocal offset
                packets = list(copied['held'])
                while offset < len(packets):
                    raw = packets[offset][1]
                    offset += 1
                    wire = raw[(raw[0] & 15)*4+8:]
                    receiver.proc.stdin.write(json.dumps(dict(op='open', hex=wire.hex())).encode()+b'\n')
                    report = receiver.read()
                    assert report['opened']
                    data = bytes.fromhex(report['hex'])
                    if report['kind'] == 0:
                        assert data == encode(hops, payload, cursor=1), 'route or opaque payload changed in transit'
                        return True
                return False
            lab.wait(arrived, 'opaque data forwarded without IP parsing')
        receiver.finish()
        origin.finish()


lib.main(test)
