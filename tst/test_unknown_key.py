"""A real mesh node changes its identity; old registry holders reject it."""

import time

import lib


def test():
    with lib.Lab(['a', 'b', 'c'], {1: ['a', 'b', 'c']}) as lab:
        lab.wait_links('a', ['b', 'c'])
        lab.wait_links('b', ['a', 'c'])
        lab.wait_links('c', ['a', 'b'])
        lab.stop_node('c')
        lab.keygen(lab.nodes['c'])
        attempt = lab.intercept('c', 'b', 'copy', kind=4)
        lab.start_node('c')
        lab.wait(lambda: attempt['hits'] == 1, 'unknown key packet actually sent')
        lab.wait_links('b', ['a'], timeout=30)
        lab.wait_links('a', ['b'], timeout=30)
        assert lab.links('c') == set()
        # More gossips from the changed identity must remain rejected.
        until = time.monotonic() + 6
        while time.monotonic() < until:
            assert lab.links('c') == set()
            lab.wait_ping('a', 'b')
            time.sleep(.2)


lib.main(test)
