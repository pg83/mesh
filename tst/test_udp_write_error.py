"""A refused datagram is a lost datagram: the UDP channel keeps its socket and is not re-dialled.

Linux sends to a bound interface whatever the routing table says, so only an IPv6 unreachable route
refuses a datagram at sendmsg, the way a macOS host down on the segment does."""
import time
import lib


def test():
    lab = lib.Lab(['a', 'b'], {1: ['a', 'b']}, ipv6=[1])
    with lab:
        lab.wait_ping('a', 'b')

        def described():
            states = [lab.raw_status(name) for name in ['a', 'b']]
            return all(len(state['channels']) == 2
                       and all(str(channel[end]) in state['addresses']
                               for channel in state['channels'] for end in ['from', 'to'])
                       for state in states)

        lab.wait(described, 'both UDP directions have their gossip descriptions')
        target = lab.nodes['b'].addresses[1]

        def channel():
            return lab.outgoing_channel('a', lab.nodes['a'].addresses[1], target)

        initial = channel()
        assert initial, 'no UDP channel towards b'
        before = lab.status('a')['addresses']
        lab.run('a', ['ip', '-6', 'route', 'add', 'unreachable', target + '/128'])
        for _ in range(7):
            time.sleep(1)
            assert channel() == initial, 'failed UDP write replaced the channel'
            current = lab.status('a')['addresses']
            assert set(current) == set(before), 'failed UDP writes generated graph vertices'
        log = (lab.dir / 'a.log').read_bytes().decode(errors='replace')
        assert 'channel write failed' not in log, 'a refused datagram closed the channel'
        lab.run('a', ['ip', '-6', 'route', 'del', 'unreachable', target + '/128'])
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')
        assert channel() == initial, 'recovered path must keep the same UDP channel'


lib.main(test)
