"""08: All mesh processes restart while the original SSH application stays alive."""
import lib
import workload


def test():
    with lib.Lab(['a', 'r', 'b'], {1: ['a', 'r'], 2: ['r', 'b']}) as lab:
        lab.wait_route('a', 'b', ['r', 'b'])
        stream = workload.SshServer(lab, 'b').stream('a')
        before = stream.replies
        for name in lab.nodes:
            lab.stop_node(name)
        for name in reversed(lab.nodes):
            lab.start_node(name)
        lab.wait_route('a', 'b', ['r', 'b'], timeout=10)
        stream.progress(after=before + 2)
        stream.finish()


lib.main(test)
