"""05: A congested QUIC channel cannot starve SSH on another peer's channel."""
import time
import lib
import workload


def test():
    with lib.Lab(['a', 'b', 'c'], {1: ['a', 'b'], 2: ['a', 'c']}) as lab:
        lab.wait_ping('a', 'b')
        lab.wait_ping('a', 'c')
        limits = [lab.intercept(src, dst, 'pace', count=-1, seg=1, rate=128 * 1024)
                  for src, dst in [('a', 'b'), ('b', 'a')]]
        server = workload.QuicServer(lab, 'b')
        client = server.client('a', seconds=20)
        stream = workload.SshServer(lab, 'c').stream('a')
        client.start()
        for _ in range(6):
            time.sleep(1)
            stream.progress(timeout=5)
            assert lab.nodes['c'].index in lab.links('a')
        client.progress()
        stream.finish()
        assert stream.max_gap < 5, stream.max_gap
        server.finish([client])
        assert all(rule['hits'] > 100 for rule in limits)
        assert sum(v for k, v in lab.counts.items() if k[-1] == 'queue-full') > 0


lib.main(test)
