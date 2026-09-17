"""A node refused on every twentieth websocket read and write still keeps the link, by dialling again."""
import os

import lib
import ws

# A broken connection is a normal event for this transport, so the points that
# break it belong here rather than in the suite at large: a scenario that
# asserts a working connection is never replaced has nothing to say once the
# connection is deliberately broken.
REFUSALS = 'ws read:20,ws write:20'


def test():
    lab = ws.Lab(('a', 'b'), statics=['a'])
    lab.node_env['b'] = {'MESH_CHAOS': REFUSALS, 'MESH_CHAOS_SEED': '3'}
    with lab:
        lab.wait_ping('a', 'b', timeout=45)
        # What has to hold is the link, not any one connection under it.
        for _ in range(4):
            lab.wait_ping('a', 'b', timeout=30)
            lab.wait_ping('b', 'a', timeout=30)
        assert lab.status('b')['links'], 'the refused node ended up with no link'

        # A connection refused for a moment is dialled again; a listener that
        # cannot take connections at all is the end of the node.
        if os.environ.get('MESH_CHAOS'):
            lab.stop_node('b')
            result = lab.run('b', [lib.MESH, 'run', '-c', lab.dir / 'b.json'], check=False, timeout=20,
                             env={**os.environ, 'MESH_CHAOS': 'accept lost:1'})

            assert result.returncode != 0 and 'invalid argument' in result.stderr, result


lib.main(test)
