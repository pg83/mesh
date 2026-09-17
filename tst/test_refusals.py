"""One node is refused by the kernel at every turn it is meant to survive, and the mesh forms anyway."""
import os
import time

import lib

# Only the points a node has no right to die on. One scan of the interfaces is
# several calls and is retried as a whole, so the period for those has to be
# well above the number of calls in a scan: a node refused every other call
# never assembles a picture at all, which says nothing about recovery and
# everything about the rate being wrong. Against the ordinary binary none of
# this means anything and the scenario is a plain two node mesh.
REFUSALS = ','.join([
    # Everything is armed, most of it so rarely that a scenario this short
    # never reaches it; the points below are the ones meant to be met.
    'all:5000',
    'implicit socket:3',
    'interface addresses:20',
    'interface event:5',
    'interfaces:20',
    'routes:5',
    'socket read:20',
    'udp write:20',
])


def test():
    lab = lib.Lab(['a', 'b'], {1: ['a', 'b']}, statics=['a'])
    # b listens on the one address it has, so a second address needs a socket
    # of its own, which is one more thing the kernel can refuse.
    lab.configs['b'] = dict(endpoint=[lib.endpoint(lib.segaddr(1, 2))])
    lab.node_env['b'] = {'MESH_CHAOS': REFUSALS, 'MESH_CHAOS_SEED': '11'}
    # a runs the very same binary with nothing armed, which is how the ordinary
    # one behaves and has to keep behaving.
    lab.node_env['a'] = {'MESH_CHAOS': None}
    with lab:
        # It takes longer than a healthy node would, and it happens.
        lab.wait_ping('a', 'b', timeout=45)
        lab.wait_ping('b', 'a', timeout=45)
        # The node that was refused is whole: it answers, it holds the link,
        # and it carries traffic both ways.
        status = lab.status('b')
        assert status['links'], status
        # A refused read loses the odd datagram, so what has to hold is the
        # link, not every single packet through it.
        for _ in range(3):
            lab.wait_ping('a', 'b', timeout=20)
            lab.wait_ping('b', 'a', timeout=20)
        # Addresses arriving and leaving make the kernel talk to the node, and
        # every one of those notifications may be refused as well. Each address
        # also wants a socket of its own, which is one more thing to refuse.
        for i in range(3):
            address = f'10.1.0.{240 + i}'

            lab.add_address('b', 1, address)
            # The node works off a one second clock, so an address that comes
            # and goes inside a tick was never really offered to it.
            time.sleep(2)
            lab.wait_ping('a', 'b', timeout=20)
            lab.run('b', ['ip', 'addr', 'del', lib.prefix(address), 'dev', 's1'])

            with lab.lock:
                lab.ports.pop((1, lib.ipbytes(address)), None)

        lab.wait_ping('a', 'b', timeout=30)
        # The tool doing the refusing has to be strict about what it is told:
        # a point that does not exist, or a rate of nothing, would leave a run
        # quietly testing less than it says it does.
        if os.environ.get('MESH_CHAOS'):
            # A device that refuses is not something to sit through: the node
            # says what happened and stops, rather than running without one.
            lab.stop_node('b')
            result = lab.run('b', [lib.MESH, 'run', '-c', lab.dir / 'b.json'], check=False, timeout=20,
                             env={**os.environ, 'MESH_CHAOS': 'tun read:1'})

            assert result.returncode != 0 and 'input/output error' in result.stderr, result

            # A listener named in the configuration is not optional: refused
            # the address it was told to take, the node stops rather than
            # running where nobody can reach it.
            result = lab.run('b', [lib.MESH, 'run', '-c', lab.dir / 'b.json'], check=False, timeout=20,
                             env={**os.environ, 'MESH_CHAOS': 'listen packet:1'})

            assert result.returncode != 0 and 'cannot assign requested address' in result.stderr, result

            # Something that panics with a value that is not an exception of
            # ours must take the node down rather than be swallowed.
            result = lab.run('b', [lib.MESH, 'run', '-c', lab.dir / 'b.json'], check=False, timeout=20,
                             env={**os.environ, 'MESH_CHAOS': 'panic:1'})

            assert result.returncode != 0 and 'panic' in result.stderr, result

            # The stack a node serves its own address from is built once, at
            # the start, and only by a node that serves something there. One
            # that cannot build it says so instead of coming up half made.
            result = lab.run('b', [lib.MESH, 'run', '-c', lab.dir / 'b.json', '-dns'], check=False, timeout=20,
                             env={**os.environ, 'MESH_CHAOS': 'netstack:1'})

            assert result.returncode != 0 and 'netstack' in result.stderr, result

            lab.start_node('b')
            lab.wait_ping('a', 'b', timeout=45)

            for spec, message in [('nonsense:5', 'unknown chaos point'),
                                  ('tun read:0', 'needs a rate above zero')]:
                result = lab.run('b', [lib.MESH, 'run', '-c', lab.dir / 'b.json'], check=False,
                                 timeout=10, env={**os.environ, 'MESH_CHAOS': spec})

                assert result.returncode != 0 and message in result.stderr, (spec, result.stderr)



lib.main(test)
