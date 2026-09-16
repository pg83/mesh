"""A DNS pool discovers its members through registry gossip after the DNS node has started."""
import json
import socket

import lib
from dns import ask


class Lab(lib.Lab):
    def write_config(self, node):
        path = super().write_config(node)
        if node.name == 'a':
            config = json.loads(path.read_text())
            config['registry'] = [p for p in config['registry'] if p['name'] != 'b']
            path.write_text(json.dumps(config))
        return path


def test():
    lab = Lab(['a', 'r', 'b'], {1: ['a', 'r'], 2: ['r', 'b']})
    lab.configs['a'] = dict(dns=True, dns_records={'*.lab': ['b']})
    held = lab.intercept('r', 'a', 'drop', kind=2, count=-1)
    with lab:
        lab.wait_ping('a', 'r')
        lab.wait_ping('r', 'b')
        assert all(p['name'] != 'b' for p in lab.status('a')['registry'])
        service = lib.SUBNET.split('/')[0]
        lab.spawn('a', [lib.MESH, 'dns', '-listen', '127.0.0.1:5355'], 'dns')

        def resolve(address, port):
            return ask(lab, 'a', address, 'grafana.lab.mesh', port=port, ttl=5, timeout=1)

        assert resolve(service, 53) == (2, [])
        lab.wait(lambda: resolve('127.0.0.1', 5355) == (2, []), 'standalone DNS knows the empty pool')
        lab.clear(held)
        want = (0, [(1, socket.inet_aton(lib.intip(3)))])
        lab.wait(lambda: resolve(service, 53) == want and resolve('127.0.0.1', 5355) == want,
                 'both DNS modes learn the pool member', timeout=20)
        lab.wait_route('a', 'b', ['r', 'b'])


lib.main(test)
