"""20: Full-size UDP crosses 16 hops, while a 17-hop route is rejected."""
import time
import json
import lib
import workload


def test():
    names = [f'n{i}' for i in range(18)]
    segments = {i + 1: [names[i], names[i + 1]] for i in range(17)}
    lab = lib.Lab(names, segments)
    # Only adjacent interfaces are wired together. Without these exclusions
    # every node sends the growing graph toward every other segment, filling
    # the userspace switch queues with unreachable traffic on small CI runners.
    for source in lab.nodes.values():
        lab.configs[source.name] = dict(no_dial=[
            {'from': lib.segaddr(src_seg, source.index),
             'to': lib.segaddr(dst_seg, target.index)}
            for src_seg in source.segments
            for target in lab.nodes.values() if target != source
            for dst_seg in target.segments if src_seg != dst_seg
        ])
    with lab:
        lab.wait_nodes('n0', names, timeout=60)
        lab.wait_route('n0', 'n16', names[1:17], timeout=60)
        lab.wait_route('n16', 'n0', list(reversed(names[:16])), timeout=60)
        lab.wait_route('n0', 'n17', None)
        lab.wait_ping('n0', 'n16', timeout=60)
        workload.udp_server(lab, 'n16')
        observers = [lab.intercept(names[i], names[i + 1], 'observe', kind=0, count=-1) for i in range(16)]
        replies = [lab.intercept(names[i + 1], names[i], 'observe', kind=0, count=-1) for i in range(16)]
        udp = workload.UdpClient(lab, 'n0', 'n16')
        for size in (1, 1200, 1352):
            payload = b'x' * size
            started = time.monotonic()
            udp.send(payload)
            assert udp.recv(timeout=5) == payload, (
                size, [rule['hits'] for rule in observers], [rule['hits'] for rule in replies])
            print(f'16-hop UDP: {size} bytes, RTT {time.monotonic() - started:.3f}s', flush=True)
        log = workload.udp_server(lab, 'n17')
        too_far = workload.UdpClient(lab, 'n0', 'n17')
        too_far.send(b'unreachable')
        assert too_far.recv(.5) is None
        assert 'unreachable'.encode().hex() not in log.read_text()
        lab.wait_route('n17', 'n1', list(reversed(names[1:17])))
        lab.wait_route('n1', 'n17', names[2:])
        lab.wait_ping('n17', 'n1', timeout=60)
        workload.udp_server(lab, 'n1')
        reverse = workload.UdpClient(lab, 'n17', 'n1')
        reverse.send(b'y' * 1352)
        assert reverse.recv(timeout=5) == b'y' * 1352
        # This scenario assumes lossless physical links; detect accidental
        # queue overflow in the test wiring separately from a routing failure.
        for name, node in lab.nodes.items():
            channels = lab.status(name)['channels']
            assert sum(c['outgoing'] for c in channels) == len(node.segments), (name, channels)
            links = json.loads(lab.run(name, ['ip', '-s', '-j', 'link', 'show']).stdout)
            for link in links:
                if link['ifname'] in {f's{seg}' for seg in node.segments}:
                    dropped = link['stats64']['tx']['dropped']
                    assert dropped == 0, (name, link['ifname'], 'test TUN queue overflow', dropped)
        lab.check()


lib.main(test)
