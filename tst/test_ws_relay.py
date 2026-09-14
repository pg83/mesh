"""Source routes cross UDP and WebSocket hops in both directions."""
import lib
import workload


class Mixed(lib.Lab):
    def __init__(self):
        super().__init__(['a', 'r', 'b'], {1: ['a', 'r'], 2: ['r', 'b']})
        self.entries = {
            'a': [dict(proto='udp', addr='10.1.0.1', port=7000)],
            'r': [dict(proto='udp', addr='10.1.0.2', port=7000),
                  dict(proto='ws', addr='10.2.0.2', port=7100, path='/mesh')],
            'b': [dict(proto='ws', addr='10.2.0.3', port=7100, path='/mesh')],
        }
        self.configs = {name: dict(endpoint=[]) for name in self.nodes}

    def registry(self):
        registry = super().registry()
        for entry, name in zip(registry, self.nodes):
            entry['endpoint'] = self.entries[name]
        return registry


def test():
    with Mixed() as lab:
        lab.wait_route('a', 'b', ['r', 'b'])
        lab.wait_route('b', 'a', ['r', 'a'])
        def transports(source, target):
            return [hop['to' if not hop['from']['endpoint'] else 'from']['proto']
                    for hop in lab.endpoint_route(source, target)]
        # r and b also link over their implicit UDP sockets on 10.2.0.x, and that
        # link can come up before r's WebSocket connection; the WebSocket hop
        # wins the tie-break once both exist.
        lab.wait(lambda: transports('a', 'b') == ['udp', 'ws'], 'a -> b over UDP then WebSocket')
        lab.wait(lambda: transports('b', 'a') == ['ws', 'udp'], 'b -> a over WebSocket then UDP')
        for name in ['a', 'b']:
            workload.udp_server(lab, name)
        for source, target in [('a', 'b'), ('b', 'a')]:
            client = workload.UdpClient(lab, source, target)
            for size in [1, 1200, 1352]:
                payload = source.encode() * size
                client.send(payload)
                assert client.recv() == payload


lib.main(test)
