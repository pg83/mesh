"""Two vectors of one version listing different records: one is an echo of what is held, the other is not."""
import time

import lib
import work_load as workload


def stale(lab, name):
    metrics = lab.http(name, '/metrics')[2].decode()
    return next(float(line.split()[1]) for line in metrics.splitlines()
                if line.startswith('mesh_vectors_stale_total '))


def test():
    with lib.Lab(['a', 'b', 'c'], {1: ['a', 'b', 'c']}) as lab:
        for name in ['b', 'c']:
            lab.wait_ping('a', name)
        lab.wait_ping('b', 'c')
        # a is replaced by a peer that says what it is told. What it talks about
        # is c, which stays where it is: the vector of a node that went away
        # would be dropped rather than compared.
        probe = workload.Probe(lab, 'a', 'b')

        def vector():
            return next((v for v in lab.status('b')['vectors'] if v['owner'] == 3), None)

        lab.wait(vector, 'b holds a vector for c')

        # Both bundles carry the version b holds: the first lists what b holds
        # too and is an echo, the second lists something else and is not. A
        # vector that moved under the attempt would be answering by age
        # instead, so that attempt says nothing and another is made.
        for _ in range(10):
            held = vector()
            before = stale(lab, 'b')
            changed = dict(held['records'], **{'2': held['records'].get('2', 0) + 1})

            probe.send(op='versions', body=[dict(owner=3, version=held['version'], records=dict(held['records']))])
            probe.send(op='versions', body=[dict(owner=3, version=held['version'], records=changed)])
            time.sleep(0.5)

            if vector() != held:
                continue

            assert stale(lab, 'b') == before + 1, 'of two vectors of one version, exactly the echo is stale'

            break
        else:
            raise AssertionError('the vector never stood still for one attempt')


lib.main(test)
