"""UDP boundary sizes and new datagrams after an interrupted route."""

import lib
import workload


def test():
    with lib.Lab(['a', 'b', 'r'], {1: ['a', 'b'], 2: ['a', 'r'], 3: ['r', 'b']}) as lab:
        lab.wait_route('a', 'b', ['b'])
        lab.wait_route('b', 'a', ['a'])
        lab.wait_route('r', 'b', ['b'])
        workload.udp_server(lab, 'b')
        client = workload.UdpClient(lab, 'a', 'b')
        for size in (0, 1, 64, 1200, 1352, 4000, 16000):
            data = b'x' * size
            client.send(data)
            assert client.recv() == data, size
        lab.block('a', 'b')
        client.send(b'during-cut')
        assert client.recv(.2) is None
        lab.wait_route('a', 'b', ['r', 'b'])
        lab.wait_route('b', 'a', ['r', 'a'])
        client.send(b'after-cut')
        assert client.recv() == b'after-cut'
        lab.unblock('a', 'b')
        lab.wait_route('a', 'b', ['b'])
        lab.wait_route('b', 'a', ['a'])
        client.send(b'after-restore')
        assert client.recv() == b'after-restore'


lib.main(test)
