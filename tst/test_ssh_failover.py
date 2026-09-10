"""Two live SSH sessions; cut, restore and flap one client's direct channel."""

import time

import lib
import workload


def test():
    segments = {1: ['a', 'b'], 2: ['a', 'r'], 3: ['r', 'b'], 4: ['c', 'b']}
    with lib.Lab(['a', 'b', 'r', 'c'], segments) as lab:
        lab.wait_route('a', 'b', ['b'])
        lab.wait_route('r', 'b', ['b'])
        lab.wait_route('c', 'b', ['b'])
        server = workload.SshServer(lab, 'b')
        affected = server.stream('a')
        unaffected = server.stream('c')
        assert affected.identity['pid'] != unaffected.identity['pid']
        for both in (True, False, True):
            started = time.monotonic()
            relay_before = lab.traffic('r', 'b')
            lab.block('a', 'b', both=both)
            before = affected.replies
            lab.wait_route('a', 'b', ['r', 'b'])
            affected.progress(after=before + 2)
            unaffected.progress()
            assert lab.traffic('r', 'b') > relay_before
            assert unaffected.max_gap < 10, f'unaffected SSH stalled: {unaffected.max_gap}'
            print(f'failover both={both}: {time.monotonic() - started:.3f}s', flush=True)
            direct_before = lab.traffic('a', 'b')
            lab.unblock('a', 'b', both=both)
            lab.wait_route('a', 'b', ['b'])
            affected.progress()
            lab.wait(lambda: lab.traffic('a', 'b') > direct_before, 'traffic returned to direct link')
        affected.finish()
        unaffected.finish()


lib.main(test)
