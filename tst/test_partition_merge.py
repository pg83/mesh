"""09: Two active partitions change their local links and merge their new graphs."""
import lib
import workload


def test():
    names = ['a', 'l', 'x', 'r', 'b', 'y']
    segments = {1: ['a', 'l', 'x'], 2: ['l', 'r'], 3: ['r', 'b', 'y']}
    with lib.Lab(names, segments) as lab:
        lab.wait_ping('a', 'b')
        left = workload.SshServer(lab, 'x').stream('a')
        right = workload.SshServer(lab, 'y').stream('b')
        lab.block('l', 'r')
        lab.wait_links('l', ['a', 'x'])
        lab.wait_links('r', ['b', 'y'])
        lab.block('a', 'x')
        lab.block('b', 'y')
        lab.wait_route('a', 'x', ['l', 'x'])
        lab.wait_route('b', 'y', ['r', 'y'])
        left.progress()
        right.progress()
        lab.unblock('l', 'r')
        lab.wait_route('a', 'b', ['l', 'r', 'b'])
        lab.wait_route('b', 'a', ['r', 'l', 'a'])
        for name in names:
            lab.wait_nodes(name, names)
        lab.wait_ping('a', 'b')
        assert lab.route('a', 'x') == ['l', 'x']
        assert lab.route('b', 'y') == ['r', 'y']
        left.finish()
        right.finish()


lib.main(test)
