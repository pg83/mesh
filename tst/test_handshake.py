"""Handshake loss, corrupted and late responses, and replayed initiation."""

import lib


def scenario(action, kind):
    lab = lib.Lab(['a', 'b'], {1: ['a', 'b']}, statics=['b'])
    source, target = ('a', 'b') if kind == 1 else ('b', 'a')
    rule = lab.intercept(source, target, action, kind=kind)
    with lab:
        lab.wait_links('a', ['b'], timeout=30)
        lab.wait_links('b', ['a'])
        assert rule['hits'] == 1
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')
        if action in ('hold', 'copy'):
            before = (lab.dir / 'b.log').read_text().count('link up')
            lab.release(rule)
            # A ping is a round trip after replay delivery, not just process liveness.
            lab.wait_ping('a', 'b')
            assert (lab.dir / 'b.log').read_text().count('link up') == before


def test():
    for action, kind in [('drop', 1), ('drop', 2), ('corrupt', 2), ('hold', 2), ('copy', 1)]:
        scenario(action, kind)
    lab = lib.Lab(['a', 'b'], {1: ['a', 'b']}, statics=['a'])
    lab.block('a', 'b', both=False)
    init = lab.intercept('b', 'a', 'copy', kind=1)
    with lab:
        lab.wait(lambda: init['hits'] == 1, 'one-way init reached responder')
        # Observe for longer than both handshake and session timeouts: repeated
        # init messages must never create a usable session on the responder.
        import time
        deadline = time.monotonic() + 17
        while time.monotonic() < deadline:
            assert lab.links('a') == set()
            assert lab.links('b') == set()
            time.sleep(.2)
        lab.unblock('a', 'b', both=False)
        lab.wait_links('a', ['b'], timeout=35)
        lab.wait_links('b', ['a'])
        lab.wait_ping('a', 'b')


lib.main(test)
