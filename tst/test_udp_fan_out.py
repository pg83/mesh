"""Four addresses per node: gossip bursts, concurrent data, and peer restart."""
import time
import lib
import work_load as workload


def drops(lab, name):
    rows = lab.run(name, ['cat', '/proc/net/udp']).stdout.splitlines()[1:]
    return sum(int(row.split()[-1]) for row in rows)


def test():
    names = ['a', 'b', 'c']
    with lib.Lab(names, {1: names}) as lab:
        for name, node in lab.nodes.items():
            for offset in (10, 20, 30):
                lab.add_address(name, 1, f'10.1.0.{offset + node.index}')
        for name in names:
            lab.wait_links(name, [other for other in names if other != name])
            lab.wait(lambda name=name: len(lab.status(name)['links']) == 32,
                     f'{name}: all endpoint pairs discovered')
            workload.udp_server(lab, name)
        before = {name: drops(lab, name) for name in names}
        clients = [(src, dst, workload.UdpClient(lab, src, dst))
                   for src in names for dst in names if src != dst]
        for sequence in range(80):
            for src, dst, client in clients:
                payload = f'{src}-{dst}-{sequence}'.encode().ljust(1200, b'.')
                client.send(payload)
                assert client.recv() == payload
            time.sleep(.1)
        assert {name: drops(lab, name) for name in names} == before
        lab.stop_node('c')
        for name in ['a', 'b']:
            lab.wait_links(name, ['b' if name == 'a' else 'a'])
        lab.wait_ping('a', 'b')
        lab.start_node('c')
        for name in names:
            lab.wait_links(name, [other for other in names if other != name])
            lab.wait(lambda name=name: len(lab.status(name)['links']) == 32,
                     f'{name}: endpoint actors recovered after restart')
        lab.wait_ping('c', 'a')
        lab.wait_ping('b', 'c')


lib.main(test)
