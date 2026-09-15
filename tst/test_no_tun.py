"""A node without a TUN device still relays and serves SSH and DNS to peers; -sshd makes that the default."""
import socket
import struct
import lib


def query(name):
    labels = b''.join(bytes([len(l)]) + l.encode() for l in name.split('.'))
    return struct.pack('>HHHHHH', 3, 0x0100, 1, 0, 0, 0) + labels + b'\0' + struct.pack('>HH', 1, 1)


def resolve(lab, name, server, question):
    code = ('import socket, sys; s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.settimeout(3); '
            's.sendto(bytes.fromhex(sys.argv[1]), (%r, 53)); r = s.recv(512); sys.stdout.write(r[-4:].hex())' % server)
    return socket.inet_ntoa(bytes.fromhex(lab.run(name, ['python3', '-c', code, query(question).hex()]).stdout))


def metric(lab, name, key):
    return next(float(l.split()[1]) for l in lab.http(name, '/metrics')[2].decode().splitlines() if l.startswith(key + ' '))


def test():
    lab = lib.Lab(['a', 'b', 'c', 'd'], {1: ['a', 'b', 'c', 'd']})
    lab.run_args['b'] = ['-sshd', '-dns']          # SSH asked for: no device by default
    lab.run_args['c'] = ['-sshd']                  # ... unless the config names one
    lab.configs['c'] = dict(tun='mesh0')
    lab.run_args['d'] = ['-no-tun']                # explicit, without SSH
    with lab:
        lab.wait_ping('a', 'c')
        lab.wait_links('a', ['b', 'c', 'd'])
        devices = {name: lab.run(name, ['ip', '-o', 'link'], check=False).stdout.count('mesh0') for name in 'abcd'}
        assert devices == {'a': 1, 'b': 0, 'c': 1, 'd': 0}, devices
        assert {name: lab.status(name)['tun'] for name in 'abcd'} == {'a': 'mesh0', 'b': '', 'c': 'mesh0', 'd': ''}
        # b answers DNS on its mesh address across the mesh, with no interface of its own.
        assert resolve(lab, 'a', lib.intip(2), 'c.mesh') == lib.intip(3)
        # What a sends to b's system is dropped there, not delivered anywhere.
        assert not lab.ping('a', 'b')
        assert metric(lab, 'b', 'mesh_tun_dropped_total') > 0
        # The two flags exclude each other.
        result = lab.run('a', [lib.MESH, 'run', '-c', lab.dir / 'a.json', '-tun', '-no-tun'], check=False, timeout=10)
        assert result.returncode != 0 and 'exclude each other' in result.stderr, result


lib.main(test)
