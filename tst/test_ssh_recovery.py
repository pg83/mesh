"""An existing SSH survives a total outage and a mesh process restart."""

import time

import lib
import workload


def test():
    with lib.Lab(['a', 'b'], {1: ['a', 'b']}) as lab:
        lab.wait_ping('a', 'b')
        stream = workload.SshServer(lab, 'b').stream('a')
        before = stream.replies
        lab.block('a', 'b')
        lab.wait_links('a', [], timeout=30)
        lab.wait_links('b', [], timeout=30)
        assert stream.proc.poll() is None
        lab.unblock('a', 'b')
        lab.wait_links('a', ['b'])
        stream.progress(after=before + 2)
        # Restart while the other end is alive; derived keys work immediately.
        before = stream.replies
        lab.stop_node('b')
        lab.start_node('b')
        lab.wait_links('b', ['a'])
        stream.progress(after=before + 2)
        stream.finish()


lib.main(test)
