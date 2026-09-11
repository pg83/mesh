"""12: Cycles and duplicate deliveries cannot turn gossip into an unbounded flood."""
import time
import lib
import workload


def test():
    names = ['a', 'b', 'c', 'd', 'e', 'f']
    segments = {1: ['a', 'b', 'c'], 2: ['c', 'd', 'e'], 3: ['e', 'f', 'a']}
    with lib.Lab(names, segments) as lab:
        for name in names:
            lab.wait_nodes(name, names)
        lab.wait_ping('b', 'd')
        workload.udp_server(lab, 'd')
        udp = workload.UdpClient(lab, 'b', 'd')
        duplicated = lab.intercept('a', 'c', 'duplicate', kind=4, every=2, count=-1)
        delayed = lab.intercept('c', 'e', 'delay', kind=4, delay=.1, count=-1)
        def sent():
            return sum(v for key, v in lab.counts.items() if key[-1] == 'sent')
        before, started = sent(), time.monotonic()
        for index in range(20):
            payload = str(index).encode().ljust(900, b'.')
            udp.send(payload)
            assert udp.recv() == payload
        time.sleep(3)
        elapsed = time.monotonic() - started
        packets = sent() - before
        assert packets < 2 * len(names) ** 3 * (elapsed + 2), (packets, elapsed)
        assert duplicated['hits'] and delayed['hits']
        assert udp.recv(.3) is None
        print(f'gossip cycle: {packets} packets in {elapsed:.2f}s', flush=True)


lib.main(test)
