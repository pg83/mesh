"""18: A data packet must arrive on the endpoint pair recorded in its source route."""
import lib
import workload


def test():
    with lib.Lab(['a', 'b'], {1: ['a', 'b'], 2: ['a', 'b']}) as lab:
        lab.wait_ping('a', 'b')
        log = workload.udp_server(lab, 'b')
        udp = workload.UdpClient(lab, 'a', 'b')
        saved = lab.intercept('a', 'b', 'hold', kind=3, min_size=900)
        payload = b'bound-to-route-endpoints'.ljust(900, b'.')
        udp.send(payload)
        lab.wait(lambda: saved['hits'] == 1, 'original data packet held')
        other = 3 - saved['held'][0][2][2]
        lab.replay(saved, seg=other)
        assert udp.recv(.3) is None
        assert payload.hex() not in log.read_text().splitlines()
        lab.release(saved)
        assert udp.recv() == payload
        udp.send(b'new-packet')
        assert udp.recv() == b'new-packet'


lib.main(test)
