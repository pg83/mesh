"""Addresses appear and vanish under the daemon: sockets follow them, the links hold, the graph settles again."""
import lib


def test():
    with lib.Lab(['a', 'b', 'c'], {1: ['a', 'b', 'c']}, statics=['a']) as lab:
        pairs = [('a', 'b'), ('a', 'c'), ('b', 'c')]
        for src, dst in pairs:
            lab.wait_ping(src, dst)
        # An address the kernel is still proving carries no socket yet. The node
        # sees it arrive, fails to bind it, and goes on with the ones it has.
        lab.run('b', ['ip', 'addr', 'add', '2001:db8:9::9/64', 'dev', 's1'])
        assert lab.ping('a', 'b'), 'a tentative address cost the node its link'
        # Addresses come and go while the peers keep talking. Each one is a
        # socket to open, a vertex to publish and, a moment later, both to drop.
        for i in range(4):
            address = f'10.1.0.{200 + i}'
            lab.add_address('b', 1, address)
            assert lab.ping('a', 'b'), f'{address} cost the node its link'
            lab.run('b', ['ip', 'addr', 'del', lib.prefix(address), 'dev', 's1'])
            with lab.lock:
                lab.ports.pop((1, lib.ipbytes(address)), None)
        # A peer restarts in the middle of it, so dials land on a node that is
        # no longer the one they were started for.
        lab.stop_node('c')
        lab.add_address('b', 1, '10.1.0.210')
        lab.start_node('c')
        lab.run('b', ['ip', 'addr', 'del', lib.prefix('10.1.0.210'), 'dev', 's1'])
        with lab.lock:
            lab.ports.pop((1, lib.ipbytes('10.1.0.210')), None)
        for src, dst in pairs:
            lab.wait_ping(src, dst, timeout=30)
        # The addresses that went away are gone from what the node advertises.
        gone = {f'10.1.0.{200 + i}' for i in range(4)} | {'10.1.0.210'}
        vertices = [v for r in lab.status('a')['records'] for v in r.get('vertices', [])]
        assert not [v for v in vertices if v.get('addr') in gone], vertices


lib.main(test)
