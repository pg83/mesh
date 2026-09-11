"""Two isolated LANs communicate through public IP/port mappings from the registry."""
import struct
import lib
import nat
import workload


def test():
    with nat.Lab() as lab:
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')
        assert lab.selected_endpoint('a', 'b') in ('198.51.100.2:17001', '198.51.100.2:17002')
        assert lab.selected_endpoint('b', 'a') == '198.51.100.1:18001'
        for name in ['a', 'b']:
            workload.udp_server(lab, name)
        clients = [workload.UdpClient(lab, 'a', 'b'), workload.UdpClient(lab, 'b', 'a')]
        captured = lab.intercept('a', 'b', 'copy', kind=3, count=-1)
        for i, client in enumerate(clients):
            payload = bytes([i + 1]) * 1100
            client.send(payload)
            assert client.recv() == payload
        assert captured['held']
        for _, packet, _ in captured['held']:
            assert packet[12:16] == bytes([198, 51, 100, 1])
            assert packet[16:20] == bytes([10, 2, 0, 2])
            src, dst = struct.unpack_from('!HH', packet, (packet[0] & 15) * 4)
            assert src == 18001 and dst in (7001, 7002)
        # The receiving graph names public endpoints, not translated socket pairs.
        for name in ['a', 'b']:
            links = lab.status(name)['links']
            assert links
            for edge in links:
                assert lib.endpoint_address(edge['from']).startswith('198.51.100.')
                assert lib.endpoint_address(edge['to']).startswith('198.51.100.')
        selected = int(lab.selected_endpoint('a', 'b').split(':')[1])
        lab.cut_port(selected - 10000)
        other = 17002 if selected == 17001 else 17001
        lab.wait(lambda: lab.selected_endpoint('a', 'b') == f'198.51.100.2:{other}',
                 'second forwarded port')
        for client in clients:
            client.send(b'after-port-failure')
            assert client.recv() == b'after-port-failure'


lib.main(test)
