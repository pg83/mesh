"""Delivery needs the directed endpoint-to-internal-IP attachment, not its reverse."""
import time
import lib
import workload


def test():
    lab = lib.Lab(['a', 'b', 'r'], {1: ['a', 'b', 'r']}, statics=['a'])
    lab.configs['b'] = dict(endpoint=[lib.endpoint('0.0.0.0')])
    with lab:
        lab.wait_route('a', 'b', ['b'])
        lab.wait_route('b', 'a', ['a'])
        workload.udp_server(lab, 'b')
        udp = workload.UdpClient(lab, 'a', 'b')
        probe = workload.Probe(lab, 'r', 'a')
        source = lab.channel_source('a', lab.nodes['a'].addresses[1], lab.nodes['b'].addresses[1])
        target = lib.endpoint(lab.nodes['b'].addresses[1])
        internal = lib.endpoint(lib.intip(lab.nodes['b'].index), 0)
        lab.intercept('a', 'b', 'drop', kind=4, count=-1)
        probe.send(op='ad', body=dict(edges=[lib.edge(internal, target, time.time_ns(), alive=False)]))
        lab.wait(lambda: not any(e['from'] == internal and e['to'] == target
                                 for e in lab.status('a')['graph']), 'outward attachment withdrawn')
        path = lab.endpoint_route('a', 'b')
        assert path == [{'from': source, 'to': target}], path
        assert any(e['from'] == target and e['to'] == internal for e in lab.status('a')['graph'])
        udp.send(b'one-way-attachment')
        assert udp.recv() == b'one-way-attachment'
        probe.finish()


lib.main(test)
