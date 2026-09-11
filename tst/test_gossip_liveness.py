"""Gossip probes every endpoint each second; data refreshes five-second liveness."""

import time

import lib
import workload


def test():
    with lib.Lab(['a', 'b'], {1: ['a', 'b'], 2: ['a', 'b']}) as lab:
        lab.wait_ping('a', 'b')
        workload.udp_server(lab, 'b')
        client = workload.UdpClient(lab, 'a', 'b')
        def traffic(seconds):
            deadline = time.monotonic() + seconds
            while time.monotonic() < deadline:
                payload = str(time.monotonic_ns()).encode().ljust(900, b'.')
                client.send(payload)
                assert client.recv() == payload
                time.sleep(.1)
        # Gossip fits below 900 bytes here; application datagrams exceed it.
        probes = [lab.intercept('a', 'b', 'copy', kind=3, seg=seg, count=-1,
                                min_size=116, max_size=899) for seg in (1, 2)]
        before = [rule['hits'] for rule in probes]
        traffic(3.2)
        assert all(rule['hits'] - start >= 3 for rule, start in zip(probes, before)), probes
        for rule in probes:
            lab.clear(rule)
        dropped = lab.intercept('a', 'b', 'drop', kind=3, count=-1, max_size=899)
        up_before = (lab.dir / 'b.log').read_text().count('link up')
        traffic(6.2)
        assert lab.status('b')['links'][0]['idle'] < 2
        assert (lab.dir / 'b.log').read_text().count('link up') == up_before
        assert dropped['hits'] >= 6

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
