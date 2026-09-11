"""03: Four loaded QUIC connections survive loss and restoration of their direct paths."""
import time
import lib
import workload


def test():
    names = ['s', 'r', 'a', 'b', 'c', 'd']
    segments = {1: ['s', 'a', 'b', 'c', 'd'], 2: ['r', 'a', 'b', 'c', 'd'], 3: ['r', 's']}
    with lib.Lab(names, segments) as lab:
        for name in names[2:]:
            lab.wait_route(name, 's', ['s'])
        server = workload.QuicServer(lab, 's')
        clients = [server.client(n, seconds=40) for n in names[2:]]
        for client in clients:
            client.start()
        phase = time.monotonic()
        for client in clients:
            client.progress(after=phase)
        for name in names[2:]:
            lab.block(name, 's', seg=1)
        for client in clients:
            lab.wait_route(client.source, 's', ['r', 's'])
        phase = time.monotonic()
        for client in clients:
            client.progress(after=phase)
        for name in names[2:]:
            lab.unblock(name, 's', seg=1)
        for client in clients:
            lab.wait_route(client.source, 's', ['s'])
        phase = time.monotonic()
        for client in clients:
            client.progress(after=phase)
        server.finish(clients)


lib.main(test)
