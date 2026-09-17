"""Two vectors of one version listing different records: the second is news, not an echo of the first."""
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
        # a is replaced by a peer that says whatever it is told to. What it
        # talks about is c, which stays where it is: a vector of a node that
        # went away would be dropped rather than compared.
        probe = workload.Probe(lab, 'a', 'b')
        lab.wait(lambda: any(v['owner'] == 3 for v in lab.status('b')['vectors']), 'b holds a vector for c')
        held = next(v for v in lab.status('b')['vectors'] if v['owner'] == 3)
        records = dict(held['records'])

        # The same version listing the same records is an echo of what b holds.
        before = stale(lab, 'b')
        probe.send(op='versions', body=[dict(owner=3, version=held['version'], records=records)])
        lab.wait(lambda: stale(lab, 'b') > before, 'a vector b already holds is counted as stale')

        # The same version listing something else is not an echo: one version
        # can be reached by different routes, and what it lists has to be read.
        echoed = stale(lab, 'b')
        changed = dict(records, **{'2': records.get('2', 0) + 1})
        probe.send(op='versions', body=[dict(owner=3, version=held['version'], records=changed)])
        lab.wait_ping('b', 'c')

        assert stale(lab, 'b') == echoed, 'a vector listing other records was taken for an echo'


lib.main(test)
