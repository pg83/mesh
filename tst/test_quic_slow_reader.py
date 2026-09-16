"""06: A QUIC client blocked by flow control does not stop three healthy clients."""
import lib
import work_load as workload


def test():
    names = ['s', 'a', 'b', 'c', 'slow']
    with lib.Lab(names, {1: names}) as lab:
        for name in names[1:]:
            lab.wait_ping(name, 's')
        server = workload.QuicServer(lab, 's')
        clients = [server.client(n, seconds=20) for n in names[1:4]]
        slow = server.client('slow', seconds=0, slow=True)
        for client in [slow, *clients]:
            client.start()
        assert slow.read().get('event') == 'blocked'
        for client in clients:
            client.progress()
        slow.proc.stdin.write(b'read\n')
        server.finish([slow, *clients])


lib.main(test)
