"""14: Lost graph withdrawals cannot keep stale directed edges alive."""
import time
import lib
import workload


def test():
    with lib.Lab(['a', 'r', 'b'], {1: ['a', 'r'], 2: ['r', 'b']}) as lab:
        lab.wait_route('a', 'b', ['r', 'b'])
        log = workload.udp_server(lab, 'a')
        udp = workload.UdpClient(lab, 'r', 'a')
        held = lab.intercept('r', 'a', 'hold', kind=4, count=-1)
        lab.block('r', 'b')
        deadline = time.monotonic() + 7
        sent = []
        while time.monotonic() < deadline:
            payload = str(len(sent)).encode().ljust(900, b'.')
            udp.send(payload)
            sent.append(payload.hex())
            time.sleep(.1)
        received = log.read_text().splitlines()
        assert all(payload in received for payload in sent), 'data-only incoming edge stopped carrying data'
        assert held['hits'] >= 2
        assert lab.route('a', 'b') is None, 'withdrawal loss left a stale route alive'
        assert lab.links('a') == {lab.nodes['r'].index}
        lab.clear(held)
        lab.wait_route('a', 'r', ['r'], timeout=3)
        assert lab.route('a', 'b') is None
        lab.wait_ping('a', 'r')


lib.main(test)
