"""UDP routing failures preserve the socket and cannot grow the advertised graph."""
import time
import lib


def test():
    lab = lib.Lab(['a', 'b'], {1: ['a', 'b']})
    with lab:
        lab.wait_ping('a', 'b')
        def source():
            return lab.channel_source('a', lab.nodes['a'].addresses[1], lab.nodes['b'].addresses[1])
        initial = source()
        before = lab.status('a')['addresses']
        lab.run('a', ['ip', 'route', 'add', 'unreachable', lab.nodes['b'].addresses[1] + '/32'])
        for _ in range(7):
            time.sleep(1)
            assert source() == initial, 'failed UDP write replaced its socket'
            current = lab.status('a')['addresses']
            assert set(current) == set(before), 'failed UDP writes generated graph vertices'
        lab.run('a', ['ip', 'route', 'del', 'unreachable', lab.nodes['b'].addresses[1] + '/32'])
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')
        assert source() == initial, 'recovered path must keep the same UDP source port'


lib.main(test)
