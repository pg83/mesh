"""14: Periodic gossip delivers a missed withdrawal while data keeps its incoming link alive."""
import time
import lib
import workload


def test():
    with lib.Lab(['a', 'r', 'b'], {1: ['a', 'r'], 2: ['r', 'b']}) as lab:
        lab.wait_route('a', 'b', ['r', 'b'])
        lab.wait_route('r', 'a', ['a'])
        source = lib.source(lab.nodes['b'].addresses[2], lab.nodes['b'].index)
        target = lib.endpoint(lab.nodes['r'].addresses[2])
        def present():
            return any(e['from'] == source and e['to'] == target for e in lab.status('a')['graph'])
        lab.wait(present, 'incoming B to R edge reaches A')
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
        assert lab.links('a') == {lab.nodes['r'].index}
        lab.clear(held)
        lab.wait_route('a', 'r', ['r'], timeout=3)
        lab.wait(lambda: not present(), 'missed withdrawal arrives in periodic gossip')
        lab.wait_ping('a', 'r')


lib.main(test)
