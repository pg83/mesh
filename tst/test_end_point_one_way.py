"""01: An incoming-only endpoint must not steal data from a working endpoint."""
import time
import lib
import work_load as workload


def test():
    lab = lib.Lab(['a', 'b'], {1: ['a', 'b'], 2: ['a', 'b']})
    lab.block('a', 'b', seg=2, both=False)
    delayed = lab.intercept('b', 'a', 'delay', seg=2, count=-1, delay=.2)
    with lab:
        lab.wait_links('a', ['b'])
        lab.wait_links('b', ['a'])
        server = workload.SshServer(lab, 'b')
        lab.wait(lambda: delayed['hits'] >= 2, 'incoming-only endpoint keeps sending')
        time.sleep(.3)
        stream = server.stream('a')
        for _ in range(5):
            time.sleep(1)
            stream.progress(timeout=5)
        assert delayed['hits'] > 0
        assert stream.max_gap < 5
        stream.finish()
        lab.wait_ping('a', 'b')


lib.main(test)
