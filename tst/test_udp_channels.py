"""UDP receive creates no return channel; local attachments follow actual I/O."""
import time
import lib
import workload


def test():
    lab = lib.Lab(['a', 'b'], {1: ['a', 'b']}, statics=['b'])
    lab.configs['a'] = dict(endpoint=[])
    forward = lab.intercept('a', 'b', 'copy', count=-1)
    reverse = lab.intercept('b', 'a', 'copy', count=-1, min_size=1)
    with lab:
        lab.wait_links('b', ['a'])
        time.sleep(2)
        a, b = lab.status('a'), lab.status('b')
        assert 'connections' not in a and 'connections' not in b
        assert len(a['channels']) == len(b['channels']) == 1, (a['channels'], b['channels'])
        assert a['channels'][0]['outgoing'] and not b['channels'][0]['outgoing']
        assert reverse['hits'] == 0, 'receiving UDP emitted return traffic'
        assert forward['hits'] > 0
        host_a, host_b = lib.endpoint(lib.intip(1), 0), lib.endpoint(lib.intip(2), 0)
        source = lib.source('10.1.0.1', 1)
        endpoint = lib.endpoint('10.1.0.2')

        def edge(state, src, dst):
            return any(e['from'] == src and e['to'] == dst for e in state['graph'])

        assert edge(a, host_a, source) and not edge(a, source, host_a)
        assert edge(b, endpoint, host_b) and not edge(b, host_b, endpoint)
        assert edge(b, source, endpoint) and not edge(b, endpoint, source)
        # B advertises its listener before receiving anything from a new peer.
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
                assert src['proto'] == 'source' and dst['proto'] == 'udp'
            rows = lab.run(name, ['cat', '/proc/net/udp']).stdout.splitlines()[1:]
            assert all(int(row.split()[2].split(':')[1], 16) == 0 for row in rows if int(row.split()[1].split(':')[0], 16) == int.from_bytes(bytes([10, 1, 0, lab.nodes[name].index]), 'little')), 'connected UDP socket'


lib.main(test)
