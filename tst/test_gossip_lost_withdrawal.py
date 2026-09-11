"""14: Losing the link-down announcement is repaired by the next periodic gossip."""
import time
import lib
import workload


def test():
    with lib.Lab(['a', 'r', 'b'], {1: ['a', 'r'], 2: ['r', 'b']}) as lab:
        lab.wait_route('a', 'b', ['r', 'b'])
        workload.udp_server(lab, 'a')
        udp = workload.UdpClient(lab, 'r', 'a')
        held = lab.intercept('r', 'a', 'hold', kind=3, min_size=116, max_size=899, count=-1)
        lab.block('r', 'b')
        deadline = time.monotonic() + 8
        while lab.nodes['b'].index in lab.links('r'):
            assert time.monotonic() < deadline, 'r did not expire b'
            payload = b'keep-a-r-alive'.ljust(900, b'.')
            udp.send(payload)
            assert udp.recv() == payload
            time.sleep(.1)
        assert lab.route('a', 'b') == ['r', 'b'], 'withdrawal was not actually suppressed'
        assert held['hits'] >= 2
        lab.clear(held)
        lab.wait_route('a', 'b', None, timeout=3)
        assert lab.links('a') == {lab.nodes['r'].index}


lib.main(test)
