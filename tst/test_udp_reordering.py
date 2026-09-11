"""Real UDP traffic tolerates reordered packets and rejects corrupted ciphertext."""

import lib
import workload


def test():
    with lib.Lab(['a', 'b'], {1: ['a', 'b']}) as lab:
        lab.wait_ping('a', 'b')
        log = workload.udp_server(lab, 'b')
        client = workload.UdpClient(lab, 'a', 'b')
        # Large application datagrams distinguish data from gossip
        # without decrypting mesh packets in the switch.
        def payload(label):
            return label.encode().ljust(900, b'.')
        held = lab.intercept('a', 'b', 'hold', kind=3, min_size=900)
        older, newer = payload('older'), payload('newer')
        client.send(older)
        lab.wait(lambda: held['hits'] == 1, 'held data packet')
        client.send(newer)
        assert client.recv() == newer
        lab.release(held)
        assert client.recv() == older

        delayed = lab.intercept('a', 'b', 'hold', kind=3, min_size=900)
        old = payload('delayed-across-busy-channel')
        client.send(old)
        lab.wait(lambda: delayed['hits'] == 1, 'old data packet')
        for index in range(1100):
            data = payload(str(index))
            client.send(data)
            assert client.recv() == data
        lab.release(delayed)
        assert client.recv() == old
        assert old.hex() in log.read_text().splitlines()

        corrupted = lab.intercept('a', 'b', 'corrupt', kind=3, min_size=900)
        bad = payload('corrupted')
        client.send(bad)
        assert client.recv(.3) is None
        assert corrupted['hits'] == 1
        client.send(payload('healthy'))
        assert client.recv() == payload('healthy')
        assert bad.hex() not in log.read_text().splitlines()


lib.main(test)
