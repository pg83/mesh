"""Registry discovery and public-key replacement over existing WS transports."""
import json

import lib
import ws


class RegistryWS(ws.Lab):
    revision = 20

    def write_config(self, node):
        path = super().write_config(node)
        config = json.loads(path.read_text())
        if node.name == 'a':
            config['registry_version'] = self.revision
        else:
            config['registry'] = [p for p in config['registry'] if p['name'] in ('a', node.name)]
        path.write_text(json.dumps(config))
        return path


def test():
    with RegistryWS(names=('a', 'b', 'c')) as lab:
        lab.wait_links('b', ['a', 'c'])
        lab.wait_links('c', ['a', 'b'])
        lab.wait_ping('b', 'c')
        lab.stop_node('c')
        lab.keygen(lab.nodes['c'])
        lab.stop_node('a')
        lab.revision = 21
        lab.start_node('a')
        lab.start_node('c')
        lab.wait_ping('b', 'c')
        lab.wait_ping('c', 'b')
        lab.wait(lambda: any(p['index'] == 3 and p['version'] == 21
                            for p in lab.status('b')['registry']), 'WS key update propagated')


lib.main(test)
