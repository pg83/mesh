"""16: An unauthenticated maximum packet ID cannot advance the replay window."""
import lib
import workload


def test():
    with lib.Lab(['a', 'b'], {1: ['a', 'b']}) as lab:
        lab.wait_ping('a', 'b')
        log = workload.udp_server(lab, 'b')
        udp = workload.UdpClient(lab, 'a', 'b')
        held = lab.intercept('a', 'b', 'hold', kind=3, min_size=900)
        payload = b'valid-after-forged-counter'.ljust(900, b'.')
        udp.send(payload)
        lab.wait(lambda: held['hits'] == 1, 'valid packet captured before delivery')
        lab.replay(held, transform=lambda p: p[:3] + b'\xff' * 8 + p[11:])
        assert udp.recv(.2) is None
        lab.release(held)
        assert udp.recv() == payload
        udp.send(b'next')
        assert udp.recv() == b'next'
        assert log.read_text().splitlines().count(payload.hex()) == 1


lib.main(test)
