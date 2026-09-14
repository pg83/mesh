"""Delivery needs the directed endpoint-to-internal-IP attachment; records may add its reverse."""
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

        def attached(src, dst):
            return any(e['from'] == src and e['to'] == dst for e in lab.status('a')['graph'])

        assert attached(target, internal) and not attached(internal, target)
        outward = next(r for r in lab.status('a')['records'] if r['owner'] == lab.nodes['b'].index)
        outward['version'] = time.time_ns() + 1_000_000_000
        for v in outward['vertices']:
            if v['endpoint']:
                v['egress'] = True
        probe.send(op='graph', body=outward)
        lab.wait(lambda: attached(internal, target), 'outward attachment added by a newer record')
        path = lab.endpoint_route('a', 'b')
        assert path == [{'from': source, 'to': target}], path
        assert attached(target, internal)
        udp.send(b'one-way-attachment')
        assert udp.recv() == b'one-way-attachment'
        probe.finish()


lib.main(test)
