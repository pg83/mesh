"""01: An incoming-only endpoint must not steal data from a working endpoint."""
import lib
import workload


def test():
    lab = lib.Lab(['a', 'b'], {1: ['a', 'b'], 2: ['a', 'b']})
    lab.block('a', 'b', seg=2, both=False)
    delayed = lab.intercept('b', 'a', 'delay', seg=2, count=-1, delay=.2)
    with lab:
        lab.wait_links('a', ['b'])
        lab.wait_links('b', ['a'])
        stream = workload.SshServer(lab, 'b').stream('a')
        for _ in range(5):
            stream.progress()
        assert delayed['hits'] > 0
        assert stream.max_gap < 5
        stream.finish()
        lab.wait_ping('a', 'b')


lib.main(test)
