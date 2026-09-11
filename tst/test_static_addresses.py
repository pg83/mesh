"""Unsupported static addresses do not prevent discovery through a valid IPv4 endpoint."""
import lib
import workload


class StaticAddresses(lib.Lab):
    def registry(self):
        registry = super().registry()
        registry[1]['endpoint'] = [dict(proto='udp', addr=addr, port=7000) for addr in ['2001:db8::1', '']] + registry[1]['endpoint']
        return registry


def test():
    with StaticAddresses(['a', 'b'], {1: ['a', 'b']}, statics=['b']) as lab:
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')
        assert lab.selected_endpoint('a', 'b') == '10.1.0.2:7000'
        workload.udp_server(lab, 'b')
        client = workload.UdpClient(lab, 'a', 'b')
        client.send(b'valid-ipv4-endpoint')
        assert client.recv() == b'valid-ipv4-endpoint'


lib.main(test)
