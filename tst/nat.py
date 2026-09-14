"""Port-forwarded UDP endpoints used by the NAT application scenarios."""
import lib
import socket
import struct


class Lab(lib.Lab):
    def __init__(self, lan=False):
        super().__init__(['a', 'b'], {1: ['a', 'b']} if lan else {1: ['a'], 2: ['b']}, statics=[])
        self.lan = lan
        if not lan:
            self.configs = {name: dict(endpoint=[]) for name in self.nodes}

    def mappings(self, name):
        if name == 'a' and self.lan:
            return []
        node = self.nodes[name]
        return [dict(proto='udp', addr=f'198.51.100.{node.index}', port=public,
                     bind_addr=node.addresses[node.segments[0]], bind_port=local)
                for public, local in ([(18001, 8001)] if name == 'a' else [(17001, 7001), (17002, 7002)])]

    def registry(self):
        registry = super().registry()
        for entry, name in zip(registry, self.nodes):
            entry['endpoint'] = self.mappings(name)
        return registry

    def add_wire(self, node, seg):
        super().add_wire(node, seg)
        self.nsenter(node, 'ip', 'route', 'add', '198.51.100.0/24', 'dev', f's{seg}', check=True)

    def start_node(self, name):
        for entry in self.mappings(name):
            self.forward(name, self.nodes[name].segments[0], entry['addr'], entry['port'], entry['bind_port'])
        super().start_node(name)

    def cut_port(self, port):
        # Faults match the local destination after DNAT. The independent
        # outgoing channel does not use this listener port.
        return [self.intercept('a', 'b', 'drop', count=-1, target_port=port)]

    def route_packet(self, source, seg, packet):
        if not self.lan and packet[0] >> 4 == 4 and packet[9] == 17:
            port = struct.unpack_from('!H', packet, (packet[0] & 15) * 4)[0]
            local = (source, seg, packet[12:16], port)
            if local not in self.forwards.values():
                public = socket.inet_aton(f'198.51.100.{self.nodes[source].index}')
                self.forwards[(public, port)] = local
        return super().route_packet(source, seg, packet)
