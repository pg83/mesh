"""11: Source routing permits different working forward and return paths."""
import lib
import workload


def test():
    names = ['a', 'r1', 'r4', 'r2', 'r3', 'b']
    segments = {1: ['a', 'r1'], 2: ['r1', 'r2'], 3: ['r2', 'b'],
                4: ['a', 'r3'], 5: ['r3', 'r4'], 6: ['r4', 'b']}
    lab = lib.Lab(names, segments)
    for src, dst in [('r1', 'a'), ('r2', 'r1'), ('b', 'r2'), ('r3', 'r4'), ('r4', 'b'), ('a', 'r3')]:
        lab.block(src, dst, both=False)
    with lab:
        lab.wait_route('a', 'b', ['r1', 'r2', 'b'])
        lab.wait_route('b', 'a', ['r4', 'r3', 'a'])
        stream = workload.SshServer(lab, 'b').stream('a')
        stream.progress()
        path = [('a', 'r1'), ('r1', 'r2'), ('r2', 'b'), ('b', 'r4'), ('r4', 'r3'), ('r3', 'a')]
        data = [lab.intercept(src, dst, 'observe', kind=0, min_size=900, count=-1) for src, dst in path]
        workload.udp_server(lab, 'b')
        udp = workload.UdpClient(lab, 'a', 'b')
        for index in range(20):
            payload = str(index).encode().ljust(900, b'.')
            udp.send(payload)
            assert udp.recv() == payload
        assert [rule['hits'] for rule in data] == [20] * len(path), data
        stream.finish()
        lab.wait_ping('b', 'a')


lib.main(test)
