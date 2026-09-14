"""Outgoing UDP leaves the listener socket; a node without listeners gets an implicit one per address."""
import time
import lib
import workload


def test():
    lab = lib.Lab(['a', 'b'], {1: ['a', 'b']}, statics=['b'])
    lab.configs['a'] = dict(endpoint=[])
    forward = lab.intercept('a', 'b', 'copy', count=-1)
    with lab:
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')
        time.sleep(2)
        a, b = lab.status('a'), lab.status('b')
        assert 'connections' not in a and 'connections' not in b
        assert forward['hits'] > 0
        host_a, host_b = lib.endpoint(lib.intip(1), 0), lib.endpoint(lib.intip(2), 0)
        # A's implicit socket is its only UDP socket: it sends to b and b dials it back.
        source = lab.channel_source('a', '10.1.0.1')
        assert source['port'] not in (0, 7000) and source['endpoint'], source
        rows = lab.run('a', ['cat', '/proc/net/udp']).stdout.splitlines()[1:]
        ports = [int(row.split()[1].split(':')[1], 16) for row in rows if int(row.split()[1].split(':')[0], 16) != 0]
        assert ports == [source['port']], ports
        endpoint = lib.endpoint('10.1.0.2')

        def edge(state, src, dst):
            return any(e['from'] == src and e['to'] == dst for e in state['graph'])

        assert edge(a, host_a, source) and edge(a, source, host_a)
        assert edge(b, endpoint, host_b) and edge(b, host_b, endpoint)
        assert edge(b, source, endpoint) and edge(a, endpoint, source)
        lab.wait(lambda: all(len(lab.status(n)['channels']) == 2 for n in ['a', 'b']), 'two directed channels each')
        # A configured listener replaces the implicit socket as the source.
        lab.stop_node('a')
        lab.configs['a'] = dict(endpoint=[lib.endpoint('0.0.0.0')])
        lab.start_node('a')
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')
        lab.wait(lambda: all(len(lab.status(n)['channels']) == 2 for n in ['a', 'b']), 'two independent directed channels')
        workload.udp_server(lab, 'b')
        udp = workload.UdpClient(lab, 'a', 'b')
        udp.send(b'explicit-independent-return-path')
        assert udp.recv() == b'explicit-independent-return-path'
        for name in ['a', 'b']:
            state = lab.status(name)
            for channel in state['channels']:
                src, dst = [state['addresses'][str(channel[k])] for k in ['from', 'to']]
                assert src['proto'] == dst['proto'] == 'udp'
                assert src == lib.endpoint(src['addr']) and dst == lib.endpoint(dst['addr']), (src, dst)
            rows = lab.run(name, ['cat', '/proc/net/udp']).stdout.splitlines()[1:]
            mine = [row for row in rows if int(row.split()[1].split(':')[0], 16) == int.from_bytes(bytes([10, 1, 0, lab.nodes[name].index]), 'little')]
            assert all(int(row.split()[2].split(':')[1], 16) == 0 for row in mine), 'connected UDP socket'
            assert not mine, 'a socket bound to the address besides the wildcard listener'


lib.main(test)
