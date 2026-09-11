"""18: A replay arriving on another physical endpoint is still the same packet."""
import lib
import workload


def test():
    with lib.Lab(['a', 'b'], {1: ['a', 'b'], 2: ['a', 'b']}) as lab:
        lab.wait_ping('a', 'b')
        log = workload.udp_server(lab, 'b')
        udp = workload.UdpClient(lab, 'a', 'b')
        saved = lab.intercept('a', 'b', 'copy', kind=3, min_size=900)
        payload = b'once-across-both-endpoints'.ljust(900, b'.')
        udp.send(payload)
        assert udp.recv() == payload
        other = 3 - saved['held'][0][2][2]
        for _ in range(10):
            lab.replay(saved, seg=other)
        assert udp.recv(.3) is None
        assert log.read_text().splitlines().count(payload.hex()) == 1
        udp.send(b'new-packet')
        assert udp.recv() == b'new-packet'


lib.main(test)
