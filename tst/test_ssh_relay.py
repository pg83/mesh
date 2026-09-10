"""A live SSH changes from a two-hop path to three hops after a relay exits."""

import lib
import workload


def test():
    segments = {1: ['a', 'r'], 2: ['r', 'b'], 3: ['a', 'x'], 4: ['x', 'y'], 5: ['y', 'b']}
    with lib.Lab(['a', 'b', 'r', 'x', 'y'], segments) as lab:
        lab.wait_route('a', 'b', ['r', 'b'])
        lab.wait_route('x', 'y', ['y'])
        lab.wait_route('y', 'b', ['b'])
        stream = workload.SshServer(lab, 'b').stream('a')
        before = stream.replies
        carried = lab.traffic('x', 'y')
        lab.stop_node('r')
        lab.wait_route('a', 'b', ['x', 'y', 'b'])
        stream.progress(after=before + 2)
        assert lab.traffic('x', 'y') > carried
        lab.start_node('r')
        lab.wait_route('a', 'b', ['r', 'b'])
        stream.progress()
        stream.finish()


lib.main(test)
