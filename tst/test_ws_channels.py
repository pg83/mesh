"""A withdrawn WS half leaves its sibling; a dead connection closes whole; shared local directions remain."""
import time
import lib
import ws


def test():
    lab = ws.Lab(statics=['b'])
    lab.configs['a'] = dict(endpoint=[])
    with lab:
        lab.wait_ping('a', 'b')
        lab.stop_node('a')
        lab.wait(lambda: not lab.channels('b'), 'old channels closed')
        endpoint = dict(proto='ws', addr='10.1.0.2', port=7100, path='/mesh', endpoint=True)
        internal = lib.endpoint(lib.intip(2), 0)

        def local_edge(src, dst):
            return any(e['from'] == src and e['to'] == dst for e in lab.status('b')['graph'])

        def connect():
            probe = ws.Probe(lab)
            assert not probe.send(op='graph', body=lib.record(1, time.time_ns(), []), read=True)['closed']
            return probe

        for first in ['stop-send', 'stop-receive']:
            probe = connect()
            assert probe.send(op='wrap')['wrapped']
            assert not probe.send(op=first)['closed']
            if first == 'stop-send':
                assert not probe.send(op='read', read=True)['closed']
            else:
                marker = lib.socket_vertex('192.0.2.91', 9011)
                probe.send(op='graph', body=lib.record(1, time.time_ns(), [(marker, False, True)]))
                lab.wait(lambda: str(lib.record_id(1, 0)) in lab.status('b')['addresses'],
                         'send half works while read half is closed and its reader is blocked')
            last = 'stop-receive' if first == 'stop-send' else 'stop-send'
            assert probe.send(op=last)['closed']
            probe.finish()
            lab.wait(lambda: not lab.channels('b'), 'socket closes after both halves')

        one, two = connect(), connect()
        assert one.source != two.source and lib.vertex_owner(one.source) == lib.vertex_owner(two.source) == 1
        lab.wait(lambda: len(lab.channels('b')) == 4, 'four independent channels')
        lab.wait(lambda: local_edge(internal, endpoint), 'outgoing local direction added')
        assert local_edge(endpoint, internal), 'listener lost its incoming direction'
        one.send(op='raw', hex='00', text=True)
        lab.wait(lambda: len(lab.channels('b')) == 3, 'one input channel withdrawn')
        assert not one.send(op='read', read=True)['closed'], 'sibling send channel closed'
        one.finish()
        # A client that sends nothing for five seconds is dead; two keeps talking.
        two.send(op='graph', body=lib.record(1, time.time_ns(), []))
        lab.wait(lambda: len(lab.channels('b')) == 2, 'remaining pair intact')
        assert local_edge(internal, endpoint), 'closing one channel removed a shared local direction'
        lab.wait(lambda: not lab.channels('b'), 'silent client closed after five seconds', timeout=10)
        assert not local_edge(internal, endpoint), 'last sender removed the outgoing direction'
        assert local_edge(endpoint, internal), 'listener must remain discoverable without clients'
        two.finish()


lib.main(test)
