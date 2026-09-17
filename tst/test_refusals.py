"""One node is refused by the kernel at every turn it is meant to survive, and the mesh forms anyway."""
import time

import lib

# Only the points a node has no right to die on, refused often enough that the
# node meets them rather than might. Every socket it opens for an address of
# its own is refused outright: it has to work with the ones it already has.
# Against the ordinary binary none of this means anything and the scenario is a
# plain two node mesh.
REFUSALS = ','.join([
    'implicit socket:1',
    'interface addresses:2',
    'interface event:2',
    'interfaces:2',
    'routes:2',
    'socket read:20',
    'udp write:20',
])


def test():
    lab = lib.Lab(['a', 'b'], {1: ['a', 'b']}, statics=['a'])
    # b listens on the one address it has, so a second address needs a socket
    # of its own, which is one more thing the kernel can refuse.
    lab.configs['b'] = dict(endpoint=[lib.endpoint(lib.segaddr(1, 2))])
    lab.node_env['b'] = {'MESH_CHAOS': REFUSALS, 'MESH_CHAOS_SEED': '11'}
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


lib.main(test)
