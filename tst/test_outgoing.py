"""UDP uses unconnected ephemeral send sockets and an explicit return listener."""
import lib
import workload


def test():
    lab = lib.Lab(['client', 'server'], {1: ['client', 'server']}, statics=['server'])
    lab.configs['client'] = dict(endpoint=[lib.endpoint('0.0.0.0', 7901)])
    lab.intercept('client', 'server', 'drop', kind=1, count=1)
    with lab:
        lab.wait_ping('client', 'server')
        lab.wait_ping('server', 'client')
        path = lab.endpoint_route('client', 'server')
        assert path[0]['from']['addr'] == '10.1.0.1' and not path[0]['from']['endpoint']
        assert path[0]['to'] == lib.endpoint('10.1.0.2')
        assert lab.endpoint_route('server', 'client')[0]['to'] == lib.endpoint('10.1.0.1', 7901)
        rows = [r.split() for r in lab.run('client', ['cat', '/proc/net/udp']).stdout.splitlines()[1:]]
        assert all(int(r[2].split(':')[1], 16) == 0 for r in rows)
        ports = [int(r[1].split(':')[1], 16) for r in rows]
        assert 7901 in ports and 7000 not in ports
        assert len(ports) == 2 and len(set(ports)) == 2, ports
        assert path[0]['from']['port'] in ports and path[0]['from']['port'] != 7901
        stream = workload.SshServer(lab, 'server').stream('client')
        stream.progress()
        lab.set_address('client', 1, '10.1.0.101')
        lab.wait_ping('client', 'server')
        stream.progress(timeout=20)
        stream.finish()


lib.main(test)
