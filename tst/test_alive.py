"""A node nobody hears is neither routed nor dialed by its record, and two nodes that heard each other cannot keep each other alive."""
import time

import lib


def alive(lab, name):
    return lab.status(name)['alive']


def dialing(lab, name, address):
    """Outgoing channels of the node towards the address."""
    return [c for c in lab.status(name)['channels'] if c['outgoing'] and c['wire']['addr'] == address]


def metric(lab, name, key):
    return next(float(l.split()[1]) for l in lab.http(name, '/metrics')[2].decode().splitlines() if l.startswith(key + ' '))


def test():
    # c has no static endpoint: a learns c's implicit listener from c's record and dials it.
    with lib.Lab(['a', 'c'], {1: ['a', 'c']}, statics=['a']) as lab:
        lab.wait_ping('a', 'c')
        c = lab.nodes['c'].addresses[1]
        lab.wait(lambda: dialing(lab, 'a', c), 'a dials the listener from c\'s record')
        assert alive(lab, 'a') == [1, 2]
        lab.stop_node('c')
        lab.wait(lambda: alive(lab, 'a') == [1] and not dialing(lab, 'a', c), 'c nobody hears is not dialed', timeout=15)
        assert metric(lab, 'a', 'mesh_peer_alive{peer="c"}') == 0
        # The record of c stays, the dial does not come back while c is silent.
        assert any(r['owner'] == 2 for r in lab.status('a')['records'])
        time.sleep(4)
        assert not dialing(lab, 'a', c), 'dialed a node nobody hears'
        lab.start_node('c')
        lab.wait(lambda: dialing(lab, 'a', c) and alive(lab, 'a') == [1, 2], 'c is dialed again once heard', timeout=15)
        lab.wait_ping('a', 'c')

    # b and c hear each other; their last records must not keep each other alive after both are gone.
    with lib.Lab(['a', 'b', 'c'], {1: ['a', 'b', 'c']}) as lab:
        for src, dst in [('a', 'b'), ('a', 'c'), ('b', 'c'), ('c', 'b')]:
            lab.wait_ping(src, dst)
        lab.wait(lambda: 3 in lab.links('b') and 2 in lab.links('c'), 'b and c hear each other')
        lab.wait(lambda: all(any(r['owner'] == owner and r['links'] for r in lab.status('a')['records']) for owner in (2, 3)), 'a holds both records')
        lab.stop_node('b')
        lab.stop_node('c')
        lab.wait_route('a', 'b', None, timeout=15)
        lab.wait_route('a', 'c', None, timeout=15)
        assert alive(lab, 'a') == [1], alive(lab, 'a')
        assert metric(lab, 'a', 'mesh_peer_alive{peer="b"}') == 0 and metric(lab, 'a', 'mesh_peer_alive{peer="c"}') == 0


lib.main(test)
