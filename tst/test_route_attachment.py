"""Delivery needs the directed endpoint-to-internal-IP attachment; a record without it withdraws the route."""
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
        target = lib.endpoint(lab.nodes['b'].addresses[1])
        internal = lib.endpoint(lib.intip(lab.nodes['b'].index), 0)

        def attached(src, dst):
            return any(e['from'] == src and e['to'] == dst for e in lab.status('a')['graph'])

        # The listener receives and, as the source of b's outgoing channels, sends.
        assert attached(target, internal) and attached(internal, target)
        current = next(r for r in lab.status('a')['records'] if r['owner'] == lab.nodes['b'].index)
        outward = dict(current, version=current['version'] + 1, vertices=[dict(v, ingress=not v['endpoint']) for v in current['vertices']])
        probe.send(op='graph', body=outward)
        lab.wait(lambda: not attached(target, internal) and lab.route('a', 'b') is None, 'route withdrawn without the inward attachment')
        assert attached(internal, target), 'the outward attachment alone remains'
        probe.send(op='graph', body=dict(current, version=outward['version'] + 1))
        lab.wait_route('a', 'b', ['b'])
        assert lab.endpoint_route('a', 'b') == [{'from': lib.endpoint(lab.nodes['a'].addresses[1]), 'to': target}]
        udp.send(b'inward-attachment-restored')
        assert udp.recv() == b'inward-attachment-restored'
        probe.finish()


lib.main(test)
