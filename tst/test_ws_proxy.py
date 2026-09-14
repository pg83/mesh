"""TLS terminates at a real reverse proxy, with a different public address and port."""
import lib
import ws
import workload


def test():
    lab = ws.TLSLab(proxy=True)
    lab.intercept('b', 'a', 'drop', syn=True, count=-1)
    with lab:
        lab.wait_ping('a', 'b')
        route = lab.endpoint_route('a', 'b')
        assert route[0]['to'] == dict(proto='wss', addr='10.1.0.99', port=7443, path='/mesh', endpoint=True)
        for name in ['a', 'b']:
            workload.udp_server(lab, name)
        for source, target in [('a', 'b'), ('b', 'a')]:
            client = workload.UdpClient(lab, source, target)
            client.send(b'public-endpoint-through-proxy')
            assert client.recv() == b'public-endpoint-through-proxy'
        assert len(lab.channels('a')) == len(lab.channels('b')) == 2
        assert lab.shared_channels()


lib.main(test)
