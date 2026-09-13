"""UDP clients use ephemeral sockets independently of configured listeners."""
import json
import lib
import workload


def test():
    for listen in [False, True]:
        lab = lib.Lab(['client', 'server'], {1: ['client', 'server']}, statics=['server'])
        lab.configs['client'] = dict(endpoint=[lib.endpoint('0.0.0.0', 7901)] if listen else [])
        # Lose the initial server response; a repeated request must recover.
        lab.intercept('server', 'client', 'drop', kind=3, count=1)
        with lab:
            lab.wait_ping('client', 'server')
            lab.wait_ping('server', 'client')
            path = lab.endpoint_route('client', 'server')
            assert path[0]['from'] == dict(proto='source', node=1, addr='10.1.0.1', port=0)
            assert path[0]['to'] == lib.endpoint('10.1.0.2')
            # A connected UDP socket has a remote port, a listener does not.
            rows = lab.run('client', ['cat', '/proc/net/udp']).stdout.splitlines()[1:]
            connected = [r.split() for r in rows if int(r.split()[2].split(':')[1], 16) == 7000]
            assert connected and all(int(r[1].split(':')[1], 16) not in [7000, 7901] for r in connected)
            listeners = [int(r.split()[1].split(':')[1], 16) for r in rows if not int(r.split()[2].split(':')[1], 16)]
            assert listeners == ([7901] if listen else [])
            stream = workload.SshServer(lab, 'server').stream('client')
            stream.progress()
            lab.set_address('client', 1, '10.1.0.101')
            lab.wait_ping('client', 'server')
            stream.progress(timeout=20)
            stream.finish()


lib.main(test)
