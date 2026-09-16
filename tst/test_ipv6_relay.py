"""An IPv6-only node reaches an IPv4-only node through a dual-stack relay."""
import lib
import work_load as workload


def test():
    with lib.Lab(['v6', 'relay', 'v4'], {1: ['v6', 'relay'], 2: ['relay', 'v4']}, ipv6=[1]) as lab:
        lab.wait_ping('v6', 'v4')
        lab.wait_ping('v4', 'v6')
        path = lab.endpoint_route('v6', 'v4')
        assert len(path) == 2, path
        assert ':' in path[0]['from']['addr'] and ':' in path[0]['to']['addr'], path
        assert ':' not in path[1]['from']['addr'] and ':' not in path[1]['to']['addr'], path
        workload.udp_server(lab, 'v4')
        udp = workload.UdpClient(lab, 'v6', 'v4')
        for size in (1, 1200, 1352):
            payload = b'x' * size
            udp.send(payload)
            assert udp.recv(timeout=5) == payload, ('IPv6 underlay with full route', size)
        stream = workload.SshServer(lab, 'v4').stream('v6')
        stream.progress()
        stream.finish()


lib.main(test)
