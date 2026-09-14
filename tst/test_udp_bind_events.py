"""A concrete UDP listener follows its address: removal closes the socket and its channels, return restores them."""
import lib


def test():
    lab = lib.Lab(['a', 'b'], {1: ['a', 'b']}, statics=['a'])
    lab.configs['b'] = dict(endpoint=[dict(proto='udp', addr='10.1.0.2', port=7000)])
    with lab:
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')

        def listening():
            rows = lab.run('b', ['cat', '/proc/net/udp']).stdout.splitlines()[1:]
            return any(int(row.split()[1].split(':')[1], 16) == 7000 for row in rows)

        assert listening()
        # A route change is a netlink message without an interface event.
        lab.run('b', ['ip', 'route', 'add', '192.0.2.0/24', 'dev', 's1'])
        lab.run('b', ['ip', 'addr', 'del', '10.1.0.2/24', 'dev', 's1'])
        lab.wait(lambda: not listening(), 'UDP listener closed by address removal', timeout=5)
        lab.wait(lambda: not lab.status('b')['channels'], 'channels closed with their address', timeout=5)
        lab.run('b', ['ip', 'addr', 'add', '10.1.0.2/24', 'dev', 's1'])
        lab.wait(listening, 'UDP listener restored by address event', timeout=5)
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')


lib.main(test)
