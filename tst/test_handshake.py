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


lib.main(test)
