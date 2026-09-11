"""A 20-second RTT carries a continuous keepalive stream and real UDP traffic."""

import time

import lib
import workload


def test():
    lab = lib.Lab(['a', 'b'], {1: ['a', 'b']}, statics=['b'])
    outgoing = lab.intercept('a', 'b', 'delay', count=-1, delay=10)
    incoming = lab.intercept('b', 'a', 'delay', count=-1, delay=10)
    with lab:
        workload.udp_server(lab, 'b')
        lab.wait_links('a', ['b'], timeout=35)
        lab.wait_links('b', ['a'])
        lab.wait_route('a', 'b', ['b'])
        lab.wait_route('b', 'a', ['a'])
        before = [(lab.dir / f'{name}.log').read_text().count('link up') for name in ('a', 'b')]
        client = workload.UdpClient(lab, 'a', 'b')
        payload = b'20-second-rtt'
        started = time.monotonic()
        client.send(payload)
        assert client.recv(timeout=35) == payload
        elapsed = time.monotonic() - started
        assert elapsed >= 20, elapsed
        assert outgoing['hits'] and incoming['hits']
        after = [(lab.dir / f'{name}.log').read_text().count('link up') for name in ('a', 'b')]
        assert before == after, (before, after)
        print(f'UDP round trip: {elapsed:.3f}s, link-up counts: {after}', flush=True)


lib.main(test)
