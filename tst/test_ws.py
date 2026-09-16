"""Independent ingress points keep two stable, bidirectional TCP connections."""
import time
import lib
import ws
import work_load as workload


def test():
    lab = ws.Lab()
    a = lab.intercept('a', 'b', 'hold', syn=True)
    b = lab.intercept('b', 'a', 'hold', syn=True)
    with lab:
        lab.wait(lambda: a['held'] and b['held'], 'both peers started a TCP connection')
        lab.release(a)
        lab.release(b)
        lab.wait_ping('a', 'b')
        lab.wait(lambda: lab.connection_count(2), 'both connections at both peers and in the kernel')
        selected = lab.channels('a')
        for name in ['a', 'b']:
            workload.udp_server(lab, name)
        clients = [workload.UdpClient(lab, 'a', 'b'), workload.UdpClient(lab, 'b', 'a')]
        for i in range(4):
            for client in clients:
                payload = str(i).encode() * 1000
                client.send(payload)
                assert client.recv() == payload
            time.sleep(1)
            assert lab.connection_count(2)
            assert lab.channels('a') == selected, 'working connection was replaced'


lib.main(test)
