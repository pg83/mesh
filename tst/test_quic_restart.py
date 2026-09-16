"""07: A busy sender restarts without reconnecting the QUIC application."""
import lib
import work_load as workload


def test():
    with lib.Lab(['a', 'b'], {1: ['a', 'b']}) as lab:
        lab.wait_ping('a', 'b')
        server = workload.QuicServer(lab, 'b')
        client = server.client('a', seconds=25)
        client.start()
        client.progress()
        lab.stop_node('a')
        assert lab.run('a', ['ip', '-j', 'link', 'show', 'mesh0']).returncode == 0
        lab.start_node('a')
        lab.wait_ping('a', 'b', timeout=4)
        client.progress()
        server.finish([client])


lib.main(test)
