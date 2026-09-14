"""Delayed packets may restore a forgotten vertex; its owner removes it again."""
import time
import lib
import workload


def test():
    with lib.Lab(['a', 'b'], {1: ['a', 'b']}) as lab:
        lab.wait_ping('a', 'b')
        old_source = lab.channel_source('b', lab.nodes['b'].addresses[1], lab.nodes['a'].addresses[1])
        held = lab.intercept('b', 'a', 'hold', kind=4, count=3)
        lab.wait(lambda: held['hits'] == 3, 'three delayed packets from the old address')
        stream = workload.SshServer(lab, 'b').stream('a')
        lab.set_address('b', 1, '10.1.0.99')
        lab.wait(lambda: lab.selected_endpoint('a', 'b') == '10.1.0.99:7000', 'new source address')
        lab.block('b', 'a', both=False)
        lab.release(held)
        time.sleep(.2)
        lab.unblock('b', 'a', both=False)
        lab.wait(lambda: lab.selected_endpoint('a', 'b') == '10.1.0.99:7000',
                 'owner refutes the delayed old attachment')
        lab.wait(lambda: str(lib.endpoint_hash(old_source)) not in lab.status('a')['addresses'],
                 'old socket is forgotten again', timeout=15)
        stream.progress()
        stream.finish()


lib.main(test)
