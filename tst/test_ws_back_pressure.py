"""A stalled WebSocket writer queues packets while the graph and another peer progress."""
import sys
import time
import lib
import ws
import work_load as workload


def test():
    with ws.Lab(names=['a', 'b', 'c'], mixed=True) as lab:
        for name in ['b', 'c']:
            lab.wait_ping('a', name)
            workload.udp_server(lab, name)
        for source, target in [('a', 'b'), ('b', 'a')]:
            lab.intercept(source, target, 'drop', target_port=7000, count=-1)
        lab.wait(lambda: (path := lab.endpoint_route('a', 'b')) and path[0]['to']['proto'] == 'ws',
                 'direct WebSocket route')
        lab.block('a', 'b')
        flood = lab.spawn('a', [sys.executable, '-c',
            "import socket,time; s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); "
            "[(s.sendto(b'x'*1000,('10.77.0.2',9000)),time.sleep(.0001)) for _ in range(10000)]"], 'ws-flood')
        client = workload.UdpClient(lab, 'a', 'c')
        for i in range(20):
            started = time.monotonic()
            lab.status('a')
            assert time.monotonic() - started < 2, 'WS writer stalls graph updates'
            payload = f'other-peer-{i}'.encode()
            client.send(payload)
            assert client.recv() == payload
            time.sleep(.1)
        assert flood.wait(timeout=10) == 0
        lab.unblock('a', 'b')
        lab.wait_ping('a', 'b')
        resumed = workload.UdpClient(lab, 'a', 'b')
        resumed.send(b'writer-resumed')
        assert resumed.recv() == b'writer-resumed'


lib.main(test)
