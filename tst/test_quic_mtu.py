"""04: An existing QUIC connection survives moving to a smaller physical MTU."""
import lib
import work_load as workload


def test():
    lab = lib.Lab(['a', 'b'], {1: ['a', 'b'], 2: ['a', 'b']})
    lab.block('a', 'b', seg=2)
    with lab:
        lab.wait_ping('a', 'b')
        large = lab.intercept('a', 'b', 'observe', count=-1, seg=1, min_size=1373)
        server = workload.QuicServer(lab, 'b')
        client = server.client('a', seconds=40)
        client.start()
        lab.wait(lambda: large['hits'] > 5, 'QUIC packets larger than the alternate MTU', timeout=15)
        for name in ('a', 'b'):
            lab.nsenter(lab.nodes[name], 'ip', 'link', 'set', 's2', 'mtu', '1400', check=True)
        lab.unblock('a', 'b', seg=2)
        lab.block('a', 'b', seg=1)
        client.progress()
        assert lab.traffic('a', 'b', seg=2) > 0
        server.finish([client])


lib.main(test)
