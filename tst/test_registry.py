"""Partial registries converge through trusted relays and enable direct links."""
import json

import lib


class SparseLab(lib.Lab):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        if 'leaf' in self.nodes:
            self.registry_block = self.intercept('relay', 'leaf', 'drop', kind=2, count=-1)

    def write_config(self, node):
        path = super().write_config(node)
        config = json.loads(path.read_text())
        if node.name == 'root':
            config['registry_version'] = 20
        else:
            seed = 'relay' if node.name == 'leaf' else 'root'
            config['registry'] = [p for p in config['registry'] if p['name'] in (node.name, seed)]
        path.write_text(json.dumps(config))
        return path


def records(lab, name):
    return {p['name']: p for p in lab.status(name)['registry']}


def test():
    with SparseLab(['root', 'a', 'b'], {1: ['root', 'a', 'b']}, statics=['root']) as lab:
        lab.wait_links('a', ['root', 'b'])
        lab.wait_links('b', ['root', 'a'])
        lab.wait_ping('a', 'b')
        lab.block('a', 'root')
        lab.block('b', 'root')
        lab.wait_ping('a', 'b')

    with SparseLab(['root', 'relay', 'leaf'], {1: ['root', 'relay'], 2: ['relay', 'leaf']},
                   statics=['root', 'relay'], ipv6=[2]) as lab:
        lab.wait_links('relay', ['root', 'leaf'])
        lab.wait_links('leaf', ['relay'])
        lab.wait(lambda: records(lab, 'relay').get('leaf', {}).get('version') == 20,
                 'relay learns the full registry')
        lab.wait(lambda: 'relay' in records(lab, 'leaf'), 'leaf control and TUN start with local config')
        lab.run('leaf', ['ip', 'addr', 'show', 'dev', 'mesh0'])
        assert 'root' not in records(lab, 'leaf')
        lab.stop_node('root')
        lab.clear(lab.registry_block)
        lab.wait(lambda: records(lab, 'leaf').get('root', {}).get('version') == 20,
                 'learned records are relayed while their source is offline')
        lab.wait_ping('leaf', 'relay')


lib.main(test)
