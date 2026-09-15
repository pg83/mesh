"""02: Previously unseen old packets cannot roll back the selected source address."""
import time
import lib
import workload


def test():
    with lib.Lab(['a', 'b'], {1: ['a', 'b']}) as lab:
        lab.wait_ping('a', 'b')
        held = lab.intercept('b', 'a', 'hold', kind=3, count=3)
        lab.wait(lambda: held['hits'] == 3, 'three delayed packets from the old address')
        stream = workload.SshServer(lab, 'b').stream('a')
        lab.set_address('b', 1, '10.1.0.99')
        lab.wait(lambda: lab.selected_endpoint('a', 'b') == '10.1.0.99:7000', 'new source address')
        lab.block('b', 'a', both=False)
        lab.release(held)
        time.sleep(.2)
        endpoint = lab.selected_endpoint('a', 'b')
        assert endpoint == '10.1.0.99:7000', endpoint
        lab.unblock('b', 'a', both=False)
        stream.progress()
        stream.finish()


lib.main(test)
