"""07: A busy sender restarts without reconnecting QUIC or replaying old UDP data."""
import lib
import workload


def test():
    with lib.Lab(['a', 'b'], {1: ['a', 'b']}) as lab:
        lab.wait_ping('a', 'b')
        log = workload.udp_server(lab, 'b', port=9001)
        udp = workload.UdpClient(lab, 'a', 'b', port=9001)
        saved = lab.intercept('a', 'b', 'copy', kind=3, min_size=900)
        original = b'old-request'.ljust(900, b'.')
        udp.send(original)
        assert udp.recv() == original
        for index in range(1100):
            payload = str(index).encode().ljust(900, b'.')
            udp.send(payload)
            assert udp.recv() == payload
        server = workload.QuicServer(lab, 'b')
        client = server.client('a', seconds=25)
        client.start()
        client.progress()
        lab.stop_node('a')
        assert lab.run('a', ['ip', '-j', 'link', 'show', 'mesh0']).returncode == 0
        lab.start_node('a')
        lab.wait_ping('a', 'b', timeout=4)
        client.progress()
        lab.release(saved)
        assert udp.recv(.3) is None
        assert log.read_text().splitlines().count(original.hex()) == 1
        server.finish([client])


lib.main(test)
