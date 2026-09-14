"""15: A reachable last endpoint is tried despite a long list of dead addresses."""
import time
import lib
import workload


class ManyEndpoints(lib.Lab):
    def registry(self):
        registry = super().registry()
        registry[1]['endpoint'] = [dict(proto='udp', addr=f'10.1.0.{i}', port=7000) for i in range(100, 132)] + registry[1]['endpoint']
        return registry


def test():
    with ManyEndpoints(['a', 'b'], {1: ['a', 'b']}, statics=['b']) as lab:
        lab.wait_ping('a', 'b', timeout=4)
        stream = workload.SshServer(lab, 'b').stream('a')
        probes = lab.intercept('b', 'a', 'observe', kind=1, count=-1)
        # The working endpoint continues receiving periodic graph updates.
        for _ in range(4):
            time.sleep(1)
            stream.progress(timeout=5)
        assert stream.max_gap < 5
        stream.finish()
        assert lab.links('a') == {lab.nodes['b'].index}
        assert probes['hits'] >= 3


lib.main(test)
