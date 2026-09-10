"""Expired node advertisements leave the database, then return after reconnection."""

import lib


def test():
    with lib.Lab(['a', 'r', 'b'], {1: ['a', 'r'], 2: ['r', 'b']}) as lab:
        lab.wait_nodes('a', ['a', 'r', 'b'])
        lab.wait_ping('a', 'b')
        lab.block('r', 'b')
        lab.wait_route('a', 'b', None)
        lab.wait_nodes('a', ['a', 'r'], timeout=55)
        lab.wait_nodes('r', ['a', 'r'], timeout=10)
        lab.unblock('r', 'b')
        lab.wait_nodes('a', ['a', 'r', 'b'], timeout=45)
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')


lib.main(test)
