"""A concrete local endpoint accepts authenticated packets only on its configured address."""
import time
import lib


def test():
    lab = lib.Lab(['a', 'b'], {1: ['a', 'b'], 2: ['a', 'b']}, statics=['a'])
    lab.configs['b'] = dict(endpoint=[dict(proto='udp', addr='10.1.0.2', port=7000)])
    with lab:
        lab.wait_ping('a', 'b')
        copied = lab.intercept('a', 'b', 'copy', kind=4, target_port=7000)
        lab.wait(lambda: bool(copied['held']), 'authenticated gossip on configured address')
        lab.replay(copied, seg=2)
        # Replaying the same ciphertext is permitted by the protocol. The
        # socket binding, rather than authentication or deduplication, rejects it.
        deadline = time.monotonic() + 2
        while time.monotonic() < deadline:
            links = lab.status('b')['links']
            assert links
            assert all(edge['to'] == lib.endpoint('10.1.0.2') for edge in links if edge['to']['proto'] == 'udp'), links
            time.sleep(.1)
        lab.wait_ping('b', 'a')


lib.main(test)
