"""13: Expiring an advertisement must not forget the newer ID already accepted."""
import time
import lib
import workload


def test():
    with lib.Lab(['a', 'r', 'b'], {1: ['a', 'r'], 2: ['r', 'b']}) as lab:
        lab.wait_nodes('a', ['a', 'r', 'b'])
        lab.stop_node('b')
        probe = workload.Probe(lab, 'r', 'a')
        old_id = time.time_ns() + 1_000_000_000
        old = dict(index=3, ts=old_id, addrs=['10.2.0.3:7000'], neighbors=[2])
        probe.send(op='ad', body=old, key=lab.nodes['b'].keys['key'])
        probe.send(op='ad', body=dict(old, ts=old_id + 1, addrs=[], neighbors=[]), key=lab.nodes['b'].keys['key'])
        deadline = time.monotonic() + 50
        while 3 in lab.status('a')['nodes']:
            assert time.monotonic() < deadline, 'advertisement did not expire'
            probe.send(op='ad', body=dict(index=2, ts=time.time_ns(), addrs=['10.1.0.2:7000'], neighbors=[1]))
            time.sleep(.5)
        probe.send(op='ad', body=old, key=lab.nodes['b'].keys['key'])
        time.sleep(.2)
        assert 3 not in lab.status('a')['nodes'], 'an expired, superseded announcement returned'
        assert lab.route('a', 'b') is None
        probe.finish()


lib.main(test)
