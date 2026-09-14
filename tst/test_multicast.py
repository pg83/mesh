"""Unreliable deletion converges through directed channels; multicast loops terminate."""
import math
import struct
import time

import lib
import workload


def message(origin, ident, vertices, hops=16):
    return struct.pack('<BHQBBH', 6, origin, ident, hops, 1, len(vertices)) + b''.join(
        struct.pack('<Q', lib.endpoint_hash(v)) for v in vertices)


def test():
    names = ['a', 'b', 'c', 'p']
    lab = lib.Lab(names, {1: names}, statics=['a', 'b', 'c'])
    cycle = [('a', 'b'), ('b', 'c'), ('c', 'a')]
    for source, target in cycle:
        lab.configs[source] = dict(no_dial=[
            {'from': f'10.1.0.{names.index(source)+1}', 'to': f'10.1.0.{names.index(other)+1}'}
            for other in ['a', 'b', 'c'] if other not in [source, target]])
    with lab:
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')
        probe = workload.Probe(lab, 'p', 'b')
        host = lib.endpoint(lib.intip(1), 0)
        stale = [lib.socket_vertex('10.1.0.1', 20000+i) for i in range(250)]
        stale_ids = {str(lib.endpoint_hash(v)) for v in stale}
        ident = time.time_ns()
        records = [lib.edge(host, v, ident, alive=i % 2 == 0) if i % 3 else
                   lib.edge(v, host, ident, alive=i % 2 == 0) for i, v in enumerate(stale)]
        records += [lib.edge(v, lib.endpoint('10.1.0.2'), ident, alive=False) for v in stale]

        def known(name, vertices):
            ids = {str(lib.endpoint_hash(v)) for v in vertices}
            return ids <= set(lab.status(name)['addresses'])

        def absent(name, ids):
            state = lab.status(name)
            return ids.isdisjoint(state['addresses'])

        delay_gossip = lab.intercept('c', 'a', 'drop', kind=4, count=-1)
        probe.send(op='ad', body=dict(edges=records))
        lab.wait(lambda: known('b', stale) and known('c', stale), 'obsolete alive and dead attachments reach two peers')
        lost = lab.intercept('a', 'b', 'hold', kind=7, count=-1)
        lab.clear(delay_gossip)
        lab.wait(lambda: lost['hits'] >= math.ceil(len(stale)/123), 'owner batches deletion across multiple packets')
        assert known('b', stale) and known('c', stale), 'held deletion reached peers'
        lost_on_relay = lab.intercept('b', 'c', 'hold', kind=7, count=-1)
        lab.clear(lost)
        lab.wait(lambda: lost_on_relay['hits'] >= math.ceil(len(stale)/123), 'relay receives and forwards deletion')
        assert known('c', stale), 'held relay deletion reached the last peer'
        lab.clear(lost_on_relay)
        lab.wait(lambda: all(absent(n, stale_ids) for n in ['a', 'b', 'c']),
                 'fresh multicast IDs pass the relay dedup cache and repair both losses', timeout=15)
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')

        # Freeze graph gossip so a duplicate delete cannot be hidden by repair.
        frozen = [lab.intercept(a, b, 'drop', kind=4, count=-1) for a, b in cycle]
        time.sleep(.4)
        sent = [lab.intercept(a, b, 'copy', kind=7, count=-1) for a, b in cycle]
        marker = lib.socket_vertex('192.0.2.9', 32000)
        ad = dict(edges=[lib.edge(lib.endpoint(lib.intip(4), 0), marker, ident)])
        probe.send(op='ad', body=ad)
        lab.wait(lambda: known('b', [marker]), 'marker installed')
        body = message(4, ident, [marker])
        for _ in range(8):
            probe.send(op='inner', hex=body.hex())
        lab.wait(lambda: all(r['hits'] == 1 for r in sent), 'multicast traverses directed cycle once')
        assert not known('b', [marker])
        time.sleep(.3)
        assert all(r['hits'] == 1 for r in sent), 'multicast was amplified by duplicates or a cycle'
        for r in sent:
            for _, packet, _ in r['held']:
                assert len(packet[(packet[0] & 15)*4+8:]) < 1300
        probe.send(op='ad', body=ad)
        lab.wait(lambda: known('b', [marker]), 'a forgotten vertex can be advertised again')
        probe.send(op='inner', hex=body.hex())
        time.sleep(.2)
        assert known('b', [marker]), 'duplicate multicast applied twice'

        # A new ID applies, with hop budget one it stays at the receiver.
        probe.send(op='inner', hex=message(4, ident+1, [marker], hops=1).hex())
        lab.wait(lambda: not known('b', [marker]), 'fresh deletion applied')
        assert all(r['hits'] == 1 for r in sent), 'hop budget one was forwarded'

        state = lab.status('b')
        live = [lib.endpoint(lib.intip(2), 0), lib.endpoint('10.1.0.2')]
        live += [state['addresses'][str(c['from'])] for c in state['channels'] if c['outgoing']]
        channels = state['channels']
        probe.send(op='inner', hex=message(4, ident+2, live, hops=1).hex())
        time.sleep(.2)
        assert known('b', live) and lab.status('b')['channels'] == channels, 'delete closed a real local socket'

        probe.send(op='ad', body=ad)
        lab.wait(lambda: known('b', [marker]), 'malformed message marker')
        malformed = [b'\6', message(0, ident, [marker]), message(4, 0, [marker]),
                     message(4, ident+3, [marker], hops=0), message(4, ident+3, [marker], hops=17),
                     message(65535, ident+3, [marker]), message(4, ident+3, [marker])*100]
        for i, bad_body in enumerate([b'', b'\1', b'\1\0\0', b'\1\1\0', b'\1\1\0'+bytes(8), b'\xff']):
            malformed.append(struct.pack('<BHQB', 6, 4, ident+10+i, 1) + bad_body)
        for inner in malformed:
            probe.send(op='inner', hex=inner.hex())
        time.sleep(.2)
        assert known('b', [marker]), 'malformed deletion changed graph'
        for r in frozen:
            lab.clear(r)
        lab.wait_ping('a', 'c')
        assert all(r['hits'] == 1 for r in sent)
        probe.finish()


lib.main(test)
