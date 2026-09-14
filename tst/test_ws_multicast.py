"""Deletion crosses WS and forgetting a live remote vertex never closes its socket."""
import struct
import time

import lib
import ws


def test():
    lab = ws.Lab(statics=['b'])
    lab.configs['a'] = dict(endpoint=[])
    with lab:
        lab.wait_ping('a', 'b')
        probe = ws.Probe(lab)
        host = lib.endpoint(lib.intip(1), 0)
        stale = lib.socket_vertex('10.1.0.1', 40000, proto='tcp')
        ident = time.time_ns()
        probe.send(op='vertices', body=[host, stale])
        record = lib.wire_ad(dict(edges=[lib.edge(host, stale, ident)]))['edges']
        probe.send(op='edges', body=record)
        key = str(lib.endpoint_hash(stale))
        lab.wait(lambda: key in lab.status('b')['addresses'], 'stale vertex advertised over WS')
        lab.wait(lambda: all(key not in lab.status(n)['addresses'] for n in ['a', 'b']),
                 'owner multicast removes stale vertex over WS')
        original = {n: lab.status(n)['channels'] for n in ['a', 'b']}
        sender = next(c for c in original['a'] if c['outgoing'])['from']
        body = struct.pack('<BHQBBHQ', 6, 1, ident, 16, 1, 1, sender)
        probe.send(op='inner', hex=body.hex())
        time.sleep(.3)
        assert all(lab.status(n)['channels'] == original[n] for n in ['a', 'b'])
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')
        assert all(lab.status(n)['channels'] == original[n] for n in ['a', 'b']), 'delete closed a live WS socket'
        probe.finish()


lib.main(test)
