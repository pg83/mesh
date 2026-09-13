"""WebSocket transport also listens and dials over IPv6-only links."""
import lib


class Lab(lib.Lab):
    def __init__(self):
        super().__init__(['a', 'b'], {1: ['a', 'b']}, ipv6=[1])
        for name in self.nodes:
            self.configs[name] = dict(endpoint=[dict(proto='ws', addr='::', port=7100)])

    def registry(self):
        registry = super().registry()
        for peer in registry:
            for ep in peer['endpoint']:
                ep.update(proto='ws', port=7100)
        return registry


def test():
    with Lab() as lab:
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')
        assert lab.status('a')['channels']
        assert lab.status('b')['channels']


lib.main(test)
