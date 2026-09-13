"""An IPv6-only node reaches an IPv4-only node through a dual-stack relay."""
import lib
import workload


def test():
    with lib.Lab(['v6', 'relay', 'v4'], {1: ['v6', 'relay'], 2: ['relay', 'v4']}, ipv6=[1]) as lab:
        lab.wait_ping('v6', 'v4')
        lab.wait_ping('v4', 'v6')
        path = lab.endpoint_route('v6', 'v4')
        assert len(path) == 2, path
        assert ':' in path[0]['from']['addr'] and ':' in path[0]['to']['addr'], path
        assert ':' not in path[1]['from']['addr'] and ':' not in path[1]['to']['addr'], path
        stream = workload.SshServer(lab, 'v4').stream('v6')
        stream.progress()
        stream.finish()


lib.main(test)
