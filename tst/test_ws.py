"""Simultaneous dials leave one real TCP connection carrying traffic both ways."""
import time
import lib
import ws
import workload


def test():
    lab = ws.Lab()
    a = lab.intercept('a', 'b', 'hold', syn=True)
    b = lab.intercept('b', 'a', 'hold', syn=True)
    with lab:
        lab.wait(lambda: a['held'] and b['held'], 'both peers started a TCP connection')
        lab.release(a)
        lab.release(b)
        lab.wait_ping('a', 'b')
        lab.wait(lab.one_connection, 'same single connection at both endpoints and in the kernel')
        selected = lab.connections('a')
        for name in ['a', 'b']:
            workload.udp_server(lab, name)
        clients = [workload.UdpClient(lab, 'a', 'b'), workload.UdpClient(lab, 'b', 'a')]
        for i in range(4):
            for client in clients:
                payload = str(i).encode() * 1000
                client.send(payload)
                assert client.recv() == payload
            time.sleep(1)
            assert lab.one_connection()
            assert lab.connections('a') == selected, 'working connection was replaced'


lib.main(test)
