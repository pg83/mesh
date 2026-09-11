"""The first endpoint loses replies; the next init in the same pass must work."""

import lib
import workload


def test():
    lab = lib.Lab(['a', 'b'], {1: ['a', 'b'], 2: ['a', 'b']}, statics=['b'])
    first = lab.intercept('a', 'b', 'hold', kind=1, seg=1)
    second = lab.intercept('a', 'b', 'hold', kind=1, seg=2)
    # Only the first two attempts may reach b; later retries cannot rescue the test.
    lab.intercept('a', 'b', 'drop', kind=1, count=-1)
    lab.block('b', 'a', seg=1, both=False)
    response = lab.intercept('b', 'a', 'copy', kind=2, seg=2)
    with lab:
        lab.wait(lambda: first['hits'] == second['hits'] == 1, 'both initial endpoints attempted')
        lab.release(first)
        lab.wait(lambda: lab.counts[('b', 'a', 1, 'dropped')] > 0, 'first init accepted, reply lost')
        lab.release(second)
        lab.wait(lambda: response['hits'] == 1, 'second init accepted', timeout=3)
        lab.wait_links('a', ['b'])
        lab.wait_links('b', ['a'])
        assert lab.status('a')['links'][0]['endpoint'] == '10.2.0.2:7000'
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')
        stream = workload.SshServer(lab, 'b').stream('a')
        stream.progress()
        stream.finish()


lib.main(test)
