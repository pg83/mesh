"""10: Delayed gossip and an in-flight obsolete source route cannot roll back a bypass."""
import lib
import workload


def test():
    segments = {1: ['a', 'r1'], 2: ['r1', 'b'], 3: ['a', 'r2'], 4: ['r2', 'b']}
    with lib.Lab(['a', 'r1', 'r2', 'b'], segments) as lab:
        lab.wait_route('a', 'b', ['r1', 'b'])
        lab.wait_route('b', 'a', ['r1', 'a'])
        log = workload.udp_server(lab, 'b')
        udp = workload.UdpClient(lab, 'a', 'b')
        ads = lab.intercept('r1', 'a', 'hold', kind=3, min_size=116, max_size=899, count=2)
        lab.wait(lambda: ads['hits'] == 2, 'old route advertisements held')
        data = lab.intercept('a', 'r1', 'hold', kind=3, min_size=900)
        old = b'obsolete-route'.ljust(900, b'.')
        udp.send(old)
        lab.wait(lambda: data['hits'] == 1, 'old source-routed packet held')
        lab.block('r1', 'b')
        lab.wait_route('a', 'b', ['r2', 'b'])
        lab.wait_route('b', 'a', ['r2', 'a'])
        observers = [lab.intercept(src, dst, 'observe', kind=3, min_size=900, count=-1)
                     for src in lab.nodes for dst in lab.nodes if src != dst]
        lab.release(ads, reverse=True)
        lab.release(data)
        assert udp.recv(.3) is None
        for index in range(20):
            payload = str(index).encode().ljust(900, b'.')
            udp.send(payload)
            assert udp.recv() == payload
        assert lab.route('a', 'b') == ['r2', 'b']
        assert old.hex() not in log.read_text().splitlines()
        assert sum(rule['hits'] for rule in observers) <= 90, 'packets looped or were multiplied'


lib.main(test)
