"""Keep the application IP; change the physical address, then lose its segment."""

import lib
import work_load as workload


def test():
    lab = lib.Lab(['a', 'b'], {1: ['a', 'b'], 2: ['a', 'b']})
    lab.block('a', 'b', seg=2)
    with lab:
        lab.wait_links('a', ['b'])
        lab.wait_links('b', ['a'])
        stream = workload.SshServer(lab, 'b').stream('a')
        lab.set_address('b', 1, '10.1.0.99')
        stream.progress()
        lab.wait(lambda: lab.selected_endpoint('a', 'b') == '10.1.0.99:7000', 'endpoint roaming')
        lab.unblock('a', 'b', seg=2)
        lab.block('a', 'b', seg=1)
        before = stream.replies
        def second_endpoint():
            return lab.selected_endpoint('a', 'b') == '10.2.0.2:7000'
        lab.wait(second_endpoint, 'second endpoint', timeout=40)
        stream.progress(after=before + 2)
        assert lab.traffic('a', 'b', seg=2) > 0
        stream.finish()


lib.main(test)
