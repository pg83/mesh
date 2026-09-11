"""Two different source endpoints to one destination have independent liveness."""
import socket
import lib
import workload


def test():
    with lib.Lab(['a', 'b'], {1: ['a', 'b']}) as lab:
        node = lab.nodes['a']
        lab.nsenter(node, 'ip', 'addr', 'add', '10.1.0.101/24', 'dev', 's1', check=True)
        with lab.lock:
            lab.ports[(1, socket.inet_aton('10.1.0.101'))] = lab.ports[(1, socket.inet_aton('10.1.0.1'))]
        lab.wait_ping('a', 'b')
        stream = workload.SshServer(lab, 'b').stream('a')
        lab.wait(lambda: (route := lab.endpoint_route('a', 'b'))
                 and route[0]['from'] == lib.endpoint('10.1.0.1'),
                 'initial route uses the source endpoint that will fail')
        lab.intercept('a', 'b', 'drop', source_ip='10.1.0.1', count=-1)
        def alternative():
            route = lab.endpoint_route('a', 'b')
            return route and route[0]['from'] == lib.endpoint('10.1.0.101')
        lab.wait(alternative, 'route uses the working source endpoint', timeout=10)
        transmitted = lab.intercept('a', 'b', 'copy', kind=3, source_ip='10.1.0.101', count=-1)
        stream.progress()
        stream.progress()
        assert transmitted['hits'] > 0
        reverse = lab.endpoint_route('b', 'a')
        assert reverse[0]['to'] == lib.endpoint('10.1.0.1'), reverse
        graph = lab.status('b')['graph']
        assert any(e['from'] == lib.endpoint('10.1.0.101') and e['to'] == lib.endpoint('10.1.0.2') for e in graph)
        assert not any(e['from'] == lib.endpoint('10.1.0.1') and e['to'] == lib.endpoint('10.1.0.2') for e in graph)
        stream.finish()


lib.main(test)
