"""Every endpoint is probed each second; data refreshes the five-second liveness."""

import time

import lib
import workload

KEEPALIVE_SIZE = 1 + 2 + 8 + 24 + 1 + 16


def test():
    with lib.Lab(['a', 'b'], {1: ['a', 'b'], 2: ['a', 'b']}) as lab:
        lab.wait_ping('a', 'b')
        probes = [lab.intercept('a', 'b', 'copy', kind=3, seg=seg, count=-1,
                                min_size=KEEPALIVE_SIZE, max_size=KEEPALIVE_SIZE) for seg in (1, 2)]
        stream = workload.SshServer(lab, 'b').stream('a')
        before = [rule['hits'] for rule in probes]
        time.sleep(3.2)
        stream.progress()
        assert all(rule['hits'] - start >= 3 for rule, start in zip(probes, before)), probes
        for rule in probes:
            lab.clear(rule)
        dropped = lab.intercept('a', 'b', 'drop', kind=3, count=-1, max_size=KEEPALIVE_SIZE)
        up_before = (lab.dir / 'b.log').read_text().count('link up')
        time.sleep(6.2)
        stream.progress()
        assert lab.status('b')['links'][0]['idle'] < 2
        assert (lab.dir / 'b.log').read_text().count('link up') == up_before
        assert dropped['hits'] >= 6
        stream.finish()

        started = time.monotonic()
        lab.block('a', 'b', both=False)
        lab.wait_links('b', [], timeout=8)
        assert time.monotonic() - started < 8
        assert lab.links('a') == {lab.nodes['b'].index}
        lab.wait_route('a', 'b', None, timeout=3)
        # Already accepted packets cannot revive a link after its idle timeout.
        lab.release(probes[0])
        time.sleep(.2)
        assert lab.links('b') == set()
        lab.clear(dropped)
        lab.unblock('a', 'b', both=False)
        lab.wait_links('b', ['a'], timeout=3)
        lab.wait_ping('a', 'b')


lib.main(test)
