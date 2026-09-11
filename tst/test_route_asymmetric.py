"""11: Source routing permits different working forward and return paths."""
import lib
import workload


def test():
    names = ['a', 'r1', 'r4', 'r2', 'r3', 'b']
    segments = {1: ['a', 'r1'], 2: ['r1', 'r2'], 3: ['r2', 'b'],
                4: ['a', 'r3'], 5: ['r3', 'r4'], 6: ['r4', 'b']}
    with lib.Lab(names, segments) as lab:
        lab.wait_route('a', 'b', ['r1', 'r2', 'b'])
        lab.wait_route('b', 'a', ['r4', 'r3', 'a'])
        stream = workload.SshServer(lab, 'b').stream('a')
        stream.progress()
        stream.finish()
        for src, dst in [('a', 'r1'), ('r1', 'r2'), ('r2', 'b'), ('b', 'r4'), ('r4', 'r3'), ('r3', 'a')]:
            assert lab.traffic(src, dst) > 0, (src, dst)
        lab.wait_ping('b', 'a')


lib.main(test)
