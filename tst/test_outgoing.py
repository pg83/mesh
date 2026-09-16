"""UDP sends from the listener socket; the channel vertex is described by that listener's address."""
import lib
import work_load as workload


def test():
    lab = lib.Lab(['client', 'server'], {1: ['client', 'server']}, statics=['server'])
    lab.configs['client'] = dict(endpoint=[lib.endpoint('0.0.0.0', 7901)])
    lab.intercept('client', 'server', 'drop', kind=1, count=1)
    with lab:
        lab.wait_ping('client', 'server')
        lab.wait_ping('server', 'client')
        path = lab.endpoint_route('client', 'server')
        assert path[0]['from'] == lib.socket_vertex('10.1.0.1', 7901), path
        assert path[0]['to'] == lib.endpoint('10.1.0.2')
        assert lab.endpoint_route('server', 'client')[0]['to'] == lib.endpoint('10.1.0.1', 7901)
        rows = [r.split() for r in lab.run('client', ['cat', '/proc/net/udp']).stdout.splitlines()[1:]]
        assert all(int(r[2].split(':')[1], 16) == 0 for r in rows)
        ports = [int(r[1].split(':')[1], 16) for r in rows]
        assert ports == [7901], ports
        # The listener itself only receives; sending is the tagged vertex.
        graph = lab.status('client')['graph']
        host = lib.endpoint(lib.intip(1), 0)
        assert any(e['from'] == lib.endpoint('10.1.0.1', 7901) and e['to'] == host for e in graph)
        assert not any(e['from'] == host and e['to'] == lib.endpoint('10.1.0.1', 7901) for e in graph)
        assert any(e['from'] == host and e['to'] == path[0]['from'] for e in graph)
        assert not any(e['from'] == path[0]['from'] and e['to'] == host for e in graph)
        stream = workload.SshServer(lab, 'server').stream('client')
        stream.progress()
        lab.set_address('client', 1, '10.1.0.101')
        lab.wait_ping('client', 'server')
        stream.progress(timeout=20)
        stream.finish()


lib.main(test)
