"""Two isolated LANs communicate through public IP/port mappings from the registry."""
import struct
import lib
import nat
import work_load as workload


def test():
    with nat.Lab() as lab:
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')
        lab.wait(lambda: lab.selected_endpoint('a', 'b') in ('198.51.100.2:17001', '198.51.100.2:17002')
                 and lab.selected_endpoint('b', 'a') == '198.51.100.1:18001',
                 'both outgoing routes through the advertised mappings')
        for name in ['a', 'b']:
            workload.udp_server(lab, name)
        clients = [workload.UdpClient(lab, 'a', 'b'), workload.UdpClient(lab, 'b', 'a')]
        captured = lab.intercept('a', 'b', 'copy', kind=0, count=-1)
        for i, client in enumerate(clients):
            payload = bytes([i + 1]) * 1100
            client.send(payload)
            assert client.recv() == payload
        assert captured['held']
        for _, packet, _ in captured['held']:
            assert packet[12:16] == bytes([198, 51, 100, 1])
            assert packet[16:20] == bytes([10, 2, 0, 2])
            src, dst = struct.unpack_from('!HH', packet, (packet[0] & 15) * 4)
            assert src == 18001 and dst in (7001, 7002), 'outgoing packets leave the listener socket'
        # The sending side describes its channel by its listener, never by a private socket.
        for channel in lab.status('a')['channels']:
            if channel['outgoing']:
                source = lab.status('a')['addresses'][str(channel['from'])]
                assert source == lib.socket_vertex('198.51.100.1', 18001) and lib.vertex_owner(channel['from']) == 1, source
        # The receiving graph names public endpoints, not translated socket pairs.
        for name in ['a', 'b']:
            links = lab.status(name)['links']
            assert links
            for edge in links:
                for side in ['from', 'to']:
                    if edge[side]['endpoint'] or edge[side]['port'] == 0:
                        assert lib.endpoint_address(edge[side]).startswith('198.51.100.')
        lab.clear(captured)
        selected = int(lab.selected_endpoint('a', 'b').split(':')[1])
        lab.cut_port(selected - 10000)
        other = 17002 if selected == 17001 else 17001
        lab.wait(lambda: lab.selected_endpoint('a', 'b') == f'198.51.100.2:{other}',
                 'second forwarded port')
        # A->B and B->A converge independently. A UDP echo needs both routes;
        # waiting for A's destination alone can lose the one-shot reply.
        lab.wait(lambda: (path := lab.endpoint_route('b', 'a'))
                 and path[0]['to'] == lib.endpoint('198.51.100.1', 18001),
                 'independent return route remains available')
        for client in clients:
            client.send(b'after-port-failure')
            assert client.recv() == b'after-port-failure'


lib.main(test)
