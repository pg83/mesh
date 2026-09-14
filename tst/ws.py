"""WebSocket endpoints on the same physical links as the UDP scenarios."""
import lib


class Lab(lib.Lab):
    def __init__(self, names=('a', 'b'), mixed=False, statics=None):
        super().__init__(list(names), {1: list(names)}, statics=statics)
        self.mixed = mixed
        for name in names:
            endpoints = [dict(proto='ws', addr='0.0.0.0', port=7100, path='/mesh')]
            if mixed:
                endpoints.append(dict(proto='udp', addr='0.0.0.0', port=7000))
            self.configs[name] = dict(endpoint=endpoints)

    def registry(self):
        registry = super().registry()
        for entry, node in zip(registry, self.nodes.values()):
            endpoints = [dict(proto='ws', addr=node.addresses[1], port=7100, path='/mesh')]
            if self.mixed:
                endpoints.append(dict(proto='udp', addr=node.addresses[1], port=7000))
            entry['endpoint'] = endpoints if node.name in self.statics else []
        return registry

    def channels(self, name):
        state = self.status(name)
        assert 'connections' not in state
        return state['channels']

    def shared_channels(self):
        a, b = self.channels('a'), self.channels('b')
        reverse_role = [dict(c, outgoing=not c['outgoing']) for c in b]
        return a == reverse_role

    def tcp_connections(self, name):
        rows = self.run(name, ['cat', '/proc/net/tcp']).stdout.splitlines()[1:]
        return sum(row.split()[3] == '01' and any(int(pair.split(':')[1], 16) == 7100
                   for pair in row.split()[1:3]) for row in rows)

    def connection_count(self, count):
        a, b = self.channels('a'), self.channels('b')
        return (len(a) == len(b) == 2*count and self.shared_channels()
                and self.tcp_connections('a') == self.tcp_connections('b') == count)

    def one_connection(self):
        return self.connection_count(1)


class TLSLab(Lab):
    def __init__(self, mixed=False, proxy=False, trusted=True, bind='10.1.0.2'):
        import subprocess
        super().__init__(mixed=mixed)
        self.proxy, self.trusted = proxy, trusted
        self.bind = bind
        self.cert, self.key = self.dir / 'cert.pem', self.dir / 'key.pem'
        subprocess.run(['openssl', 'req', '-config', '/dev/null', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                        '-subj', '/CN=mesh-test', '-addext', 'subjectAltName=IP:10.1.0.2,IP:10.1.0.99',
                        '-keyout', str(self.key), '-out', str(self.cert)],
                       check=True, capture_output=True, timeout=15)
        self.configs['b']['endpoint'] = [self.server_endpoint()]
        if mixed:
            self.configs['b']['endpoint'].append(dict(proto='udp', addr='0.0.0.0', port=7000))

    def server_endpoint(self):
        return dict(proto='wss', addr='10.1.0.99' if self.proxy else '10.1.0.2',
                    port=7443 if self.proxy else 7100, path='/mesh', bind_addr=self.bind,
                    bind_port=7101 if self.proxy else 7100, bind_proto='ws' if self.proxy else 'wss',
                    tls_cert=str(self.cert), tls_key=str(self.key), tls_ca=str(self.cert) if self.trusted else '')

    def registry(self):
        registry = super().registry()
        registry[1]['endpoint'][0] = self.server_endpoint()
        return registry

    def add_wire(self, node, seg):
        import socket
        super().add_wire(node, seg)
        if self.proxy and node.name == 'b':
            self.nsenter(node, 'ip', 'addr', 'add', '10.1.0.99/24', 'dev', 's1', check=True)
            self.ports[(1, socket.inet_aton('10.1.0.99'))] = self.ports[(1, socket.inet_aton(node.addresses[1]))]

    def start_node(self, name):
        import os
        import workload
        if self.proxy and name == 'b':
            host = f'[{self.bind}]' if ':' in self.bind else self.bind
            proc = self.spawn('b', [os.environ['MESH_TEST_PROBE'], 'proxy', '10.1.0.99:7443',
                                  f'http://{host}:7101', self.cert, self.key], 'tls-proxy')
            workload.wait_port(self, 'b', '10.1.0.99', 7443, proc)
        super().start_node(name)


class Probe:
    def __init__(self, lab, config=None):
        import os
        import subprocess
        self.proc = lab.spawn('a', [os.environ['MESH_TEST_PROBE'], config or lab.dir / 'a.json',
                                   'ws://10.1.0.2:7100/mesh', '2'], 'ws-probe',
                              stdin=subprocess.PIPE, stdout=subprocess.PIPE, bufsize=0)
        ready = self.read()
        assert ready['ready']
        self.source = ready['source']

    def read(self):
        import select
        import json
        assert select.select([self.proc.stdout], [], [], 10)[0], 'WS probe timed out'
        return json.loads(self.proc.stdout.readline())

    def send(self, **command):
        import json
        self.proc.stdin.write(json.dumps(command).encode() + b'\n')
        return self.read()

    def finish(self):
        self.proc.stdin.close()
        assert self.proc.wait(timeout=10) == 0
