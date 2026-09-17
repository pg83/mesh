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
        probe = workload.Probe(lab, 'a', 'b')

        # Keep c's periodic bundles from changing the vector under test. A
        # fresh version also supersedes bundles already in flight.
        lab.intercept('c', 'b', 'drop', kind=3, count=-1)
        version = time.time_ns()
        records = {'1': 1, '2': 1}

        def vector():
            return next((v for v in lab.status('b')['vectors'] if v['owner'] == 3), None)

        def send(entries):
            probe.send(op='versions', body=[dict(owner=3, version=version, records=entries)])

        send(records)
        lab.wait(lambda: vector() == dict(owner=3, version=version, records=records),
                 'b applies the initial vector')

        # Status sees the graph actor's state before every channel has its new
        # snapshot. An echo counted as stale proves the receiving channel has
        # caught up; sending the changed bundle can then test equal versions.
        def wait_echo(entries):
            before = stale(lab, 'b')

            def echoed():
                send(entries)
                return stale(lab, 'b') > before

            lab.wait(echoed, 'an identical vector is counted as stale')

        wait_echo(records)
        for changed in [{'2': 2}, {'3': 1}]:
            send(changed)
            records |= changed
            lab.wait(lambda: vector() == dict(owner=3, version=version, records=records),
                     'a vector of the same version merges changed and additional records')
            wait_echo(changed)
        probe.finish()


lib.main(test)
