"""A node whose own address is on its own network keeps advertising it, however it looks from the far side of a translation."""
import lib
import nat


class Lab(nat.Lab):
    """a is reachable through a translation; b and c share a network behind it
    and publish the addresses they actually have."""

    def __init__(self):
        lib.Lab.__init__(self, ['a', 'b', 'c'], {1: ['a'], 2: ['b', 'c']}, statics=['b', 'c'])
        self.lan = False
        self.configs = {'a': dict(endpoint=[])}

    def mappings(self, name):
        return super().mappings(name) if name == 'a' else []

    def registry(self):
        registry = lib.Lab.registry(self)

        for entry, name in zip(registry, self.nodes):
            if name == 'a':
                entry['endpoint'] = self.mappings(name)

        return registry


def test():
    with Lab() as lab:
        lab.wait_ping('b', 'a')
        lab.wait_ping('b', 'c')
        # a sees b arrive from the translated address, and says so.
        def reported():
            return {o['seen']['addr'] for r in lab.status('b')['records'] if r['owner'] == 1
                    for o in r.get('observed', [])}

        lab.wait(lambda: any(address.startswith('198.51.100.') for address in reported()),
                 'a reports b at the translated address', timeout=30)
        # b does not take that for its own: the address it has is on its own
        # network, and that is the one its neighbours there have to use.
        own = {v['addr'] for r in lab.status('c')['records'] if r['owner'] == 2
               for v in r.get('vertices', []) if v.get('endpoint')}

        assert own == {lab.nodes['b'].addresses[2]}, own
        assert lab.ping('c', 'b') and lab.ping('b', 'a')


lib.main(test)
