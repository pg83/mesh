"""A concrete WS listener follows IPv4/IPv6 addresses without restarting mesh or SSH."""
import lib
import work_load as workload


class Lab(lib.Lab):
    def __init__(self, ipv6):
        super().__init__(['a', 'b'], {1: ['a', 'b']}, statics=['b'], ipv6=[1] if ipv6 else [])
        self.address = '2001:db8:1::2' if ipv6 else '10.1.0.2'
        self.configs['a'] = dict(endpoint=[])
        self.configs['b'] = dict(endpoint=[dict(proto='ws', addr=self.address, port=7100, path='/mesh')])

    def registry(self):
        registry = super().registry()
        registry[1]['endpoint'] = self.configs['b']['endpoint']
        return registry


def test():
    for ipv6 in [False, True]:
        with Lab(ipv6) as lab:
            lab.wait_ping('a', 'b')
            stream = workload.SshServer(lab, 'b').stream('a')
            node = lab.nodes['b'].proc

            def listening():
                table = '/proc/net/tcp6' if ipv6 else '/proc/net/tcp'
                rows = lab.run('b', ['cat', table]).stdout.splitlines()[1:]
                return any(row.split()[3] == '0A' and int(row.split()[1].split(':')[1], 16) == 7100
                           for row in rows)

            lab.wait(listening, 'concrete WS listener ready')
            for _ in range(2):
                lab.run('b', ['ip', 'addr', 'del', lib.prefix(lab.address), 'dev', 's1'])
                lab.wait(lambda: not listening(), 'WS listener removed by address event', timeout=3)
                lab.wait(lambda: not lab.status('b')['channels'], 'local WS connections closed', timeout=3)
                assert stream.proc.poll() is None, 'SSH exited while the address was absent'
                lab.run('b', ['ip', 'addr', 'add', lib.prefix(lab.address), 'dev', 's1',
                              *(['nodad'] if ipv6 else [])])
                lab.wait(listening, 'WS listener restored by address event', timeout=3)
                lab.wait_ping('a', 'b')
                stream.progress()
                assert node is lab.nodes['b'].proc and node.poll() is None
            stream.finish()


lib.main(test)
