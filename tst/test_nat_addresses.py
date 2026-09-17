"""Two observers see different public IPs for one private listener; selection follows live observations."""
import socket
import struct

import lib


class Lab(lib.Lab):
    def __init__(self):
        super().__init__(['a', 'b', 'c'], {1: ['a'], 2: ['b'], 3: ['c']}, statics=['b', 'c'])
        self.configs = {name: dict(endpoint=[]) for name in self.nodes}

    def registry(self):
        registry = super().registry()
        for entry, name in zip(registry, self.nodes):
            if name != 'a':
                node = self.nodes[name]
                entry['endpoint'] = [dict(proto='udp', addr=f'198.51.100.{node.index}', port=7000,
                                          bind_addr=node.addresses[node.index], bind_port=7000)]
        return registry

    def add_wire(self, node, seg):
        super().add_wire(node, seg)
        self.nsenter(node, 'ip', 'route', 'add', '198.51.100.0/24', 'dev', f's{seg}', check=True)

    def start_node(self, name):
        if name != 'a':
            index = self.nodes[name].index
            self.forward(name, index, f'198.51.100.{index}', 7000, 7000)
        super().start_node(name)

    def route_packet(self, source, seg, packet):
        if source == 'a' and packet[0] >> 4 == 4 and packet[9] == 17:
            head = (packet[0] & 15) * 4
            port, = struct.unpack_from('!H', packet, head)
            # Both mappings route back to the same socket. Outbound translation
            # depends on which observer receives the packet.
            public = '198.51.100.10' if packet[16:20] == socket.inet_aton('198.51.100.2') else '198.51.100.11'
            key = (socket.inet_aton(public), port)
            local = (source, seg, packet[12:16], port)
            self.forwards[key] = local
            self.forwards = {key: local, **self.forwards}
        return super().route_packet(source, seg, packet)


def test():
    lab = Lab()
    lab.block('a', 'b')
    with lab:
        def observations():
            state = lab.status('c')
            return {o['seen']['addr'] for r in state['records']
                    for o in r['observed'] if lib.vertex_owner(o['from']) == 1}

        def selected(address):
            state = lab.status('c')
            channels = [c for c in state['channels'] if c['outgoing']
                        and lib.vertex_owner(c['to']) == 1]
            return bool(channels) and all(c['wire']['addr'] == address for c in channels)

        lab.wait_links('c', ['a', 'b'])
        lab.wait(lambda: observations() == {'198.51.100.11'} and selected('198.51.100.11'),
                 'the first observer supplies the public address')
        lab.wait_ping('c', 'a')
        lab.unblock('a', 'b')
        lab.wait(lambda: observations() == {'198.51.100.10', '198.51.100.11'} and selected('198.51.100.10'),
                 'the lower public IP wins even when it arrives second')
        lab.wait_ping('c', 'a')
        lab.block('a', 'b')
        lab.wait(lambda: observations() == {'198.51.100.11'} and selected('198.51.100.11'),
                 'withdrawal selects the remaining mapping')
        lab.wait_ping('c', 'a')
        lab.unblock('a', 'b')
        lab.wait(lambda: observations() == {'198.51.100.10', '198.51.100.11'} and selected('198.51.100.10'),
                 'return of the observer restores the preferred mapping')
        lab.wait_ping('c', 'a')


lib.main(test)
