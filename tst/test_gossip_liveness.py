"""Gossip probes every endpoint each second; data refreshes five-second liveness."""

import time

import lib
import work_load as workload


def test():
    with lib.Lab(['a', 'b'], {1: ['a', 'b'], 2: ['a', 'b']}) as lab:
        lab.wait_ping('a', 'b')
        log = workload.udp_server(lab, 'b')
        client = workload.UdpClient(lab, 'a', 'b')
        def traffic(seconds):
            deadline = time.monotonic() + seconds
            while time.monotonic() < deadline:
                payload = str(time.monotonic_ns()).encode().ljust(900, b'.')
                client.send(payload)
                assert client.recv() == payload
                time.sleep(.1)
        # Gossip and data have distinct authenticated outer packet types; the
        # periodic gossip is the version bundle.
        probes = [lab.intercept('a', 'b', 'observe', kind=3, seg=seg, count=-1,
                                max_size=899) for seg in (1, 2)]
        before = [rule['hits'] for rule in probes]
        traffic(3.2)
        assert all(rule['hits'] - start >= 3 for rule, start in zip(probes, before)), probes
        for rule in probes:
            lab.clear(rule)
        dropped = lab.intercept('a', 'b', 'drop', kind=3, count=-1)
        records_dropped = lab.intercept('a', 'b', 'drop', kind=1, count=-1)
        registry_dropped = lab.intercept('a', 'b', 'drop', kind=2, count=-1)
        up_before = (lab.dir / 'b.log').read_text().count('link up')
        # Only the incoming edge is proven by data. Send without requiring the
        # reverse route, whose graph announcements are deliberately suppressed.
        delivered = []
        deadline = time.monotonic() + 6.2
        while time.monotonic() < deadline:
            payload = str(time.monotonic_ns()).encode().ljust(900, b'.')
            client.send(payload)
            delivered.append(payload.hex())
            time.sleep(.1)
        lines = log.read_text().splitlines()
        assert all(payload in lines for payload in delivered)
        assert lab.status('b')['links'][0]['idle'] < 2
        assert (lab.dir / 'b.log').read_text().count('link up') == up_before
        assert dropped['hits'] >= 6

        started = time.monotonic()
        lab.block('a', 'b', both=False)
        lab.wait_links('b', [], timeout=8)
        assert time.monotonic() - started < 8
        assert lab.links('a') == {lab.nodes['b'].index}
        lab.wait_route('a', 'b', None, timeout=3)
        lab.clear(dropped)
        lab.clear(records_dropped)
        lab.clear(registry_dropped)
        lab.unblock('a', 'b', both=False)
        lab.wait_links('b', ['a'], timeout=3)
        lab.wait_ping('a', 'b')


lib.main(test)
