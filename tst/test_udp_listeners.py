"""Overlapping declarations share one UDP listener; explicit binds stay separate."""
import time
import lib
import work_load as workload


def test():
    for wildcard in [True, False]:
        lab = lib.Lab(['a', 'b'], {1: ['a', 'b'], 2: ['a', 'b'], 3: ['a', 'b']}, ipv6=[3])
        if not wildcard:
            lab.configs = {name: dict(endpoint=[]) for name in lab.nodes}
        with lab:
            lab.wait_ping('a', 'b')
            for name in lab.nodes:
                for family, expected in [('udp', 1 if wildcard else 2), ('udp6', 1)]:
                    rows = lab.run(name, ['cat', '/proc/net/'+family]).stdout.splitlines()[1:]
                    listeners = [row for row in rows if int(row.split()[1].split(':')[1], 16) == 7000]
                    assert len(listeners) == expected, (wildcard, family, listeners)
            time.sleep(6)
            for name in lab.nodes:
                assert len(lab.status(name)['links']) == 3, 'overlapping listener stole an established channel'
            workload.udp_server(lab, 'b')
            client = workload.UdpClient(lab, 'a', 'b')
            client.send(b'listener-remains-reachable')
            assert client.recv() == b'listener-remains-reachable'


lib.main(test)
