"""Four real QUIC clients stress one server for thirty seconds without reconnecting."""

import lib
import work_load as workload


def test():
    names = ['s', 'a', 'b', 'c', 'd']
    with lib.Lab(names, {1: names}) as lab:
        for name in names[1:]:
            lab.wait_ping(name, 's')
        server = workload.QuicServer(lab, 's')
        clients = [server.client(name) for name in names[1:]]
        for client in clients:
            client.start()
        server.finish(clients)


lib.main(test)
