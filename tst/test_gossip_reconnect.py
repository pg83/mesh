"""Gossip and traffic resume after reconnecting separated network segments."""

import lib


def test():
    with lib.Lab(['a', 'r', 'b'], {1: ['a', 'r'], 2: ['r', 'b']}) as lab:
        lab.wait_nodes('a', ['a', 'r', 'b'])
        lab.wait_ping('a', 'b')
        lab.block('r', 'b')
        lab.wait_links('r', ['a'])
        lab.wait_links('b', [])
        lab.unblock('r', 'b')
        lab.wait_nodes('a', ['a', 'r', 'b'], timeout=45)
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')


lib.main(test)
