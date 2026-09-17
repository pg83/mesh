"""Nodes answer the mesh zone on the subnet's service address and their own address; mesh dns serves it from the control API."""
import json
import socket
import lib
from dns import ask, name_of, parse, query, send


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
        # Malformed queries are dropped: short, a response, two questions, a name
        # cut mid-label, a name whose last label ends the packet with nothing to
        # close it, a label over 63 bytes, a name too long; a wrong class and a
        # bad octet in a reverse name are refused; a PTR name asked for another
        # type is empty.
        q = query('b.mesh')
        for bad in [q[:11], bytes([0, 7, 0x81]) + q[3:], q[:4] + b'\0\x02' + q[6:], q[:16], q[:14], q[:12] + b'\x40' + b'x' * 64 + q[13:],
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

        config['dns_port'] = 53
        for records, message in [
            ({'': ['a']}, 'invalid name'),
            ({'x..lab': ['a']}, 'invalid name'),
            ({'x.*.lab': ['a']}, 'invalid name'),
            ({'x/lab': ['a']}, 'invalid name'),
            ({'x' * 64: ['a']}, 'invalid name'),
            ({'.'.join(['x' * 63] * 4): ['a']}, 'name too long'),
            ({'*.lab': []}, 'no nodes'),
            ({'*.lab': ['']}, 'invalid node'),
            ({'*.lab': ['a.mesh']}, 'invalid node'),
            ({'APP': ['a'], 'app.': ['b']}, 'duplicate name'),
        ]:
            (lab.dir / 'bad.json').write_text(json.dumps(dict(config, dns_records=records)))
            result = lab.run('c', [lib.MESH, 'run', '-c', lab.dir / 'bad.json'], check=False, timeout=10)
            assert result.returncode != 0 and message in result.stderr, (records, result)


lib.main(test)
