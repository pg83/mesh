"""A retry replaces its old waiter; the delayed old response must be ignored."""

import lib
import workload


def udp_payload(packet):
    return packet[(packet[0] & 15) * 4 + 8:]


def test():
    lab = lib.Lab(['a', 'b'], {1: ['a', 'b']}, statics=['b'])
    inits = lab.intercept('a', 'b', 'copy', kind=1, count=2)
    lab.intercept('a', 'b', 'drop', kind=1, count=-1)
    first = lab.intercept('b', 'a', 'hold', kind=2)
    second = lab.intercept('b', 'a', 'hold', kind=2)
    transport = lab.intercept('a', 'b', 'copy', kind=3)
    with lab:
        lab.wait(lambda: second['hits'] == 1, 'retry reached b and produced a second response')
        assert inits['hits'] == 2 and first['hits'] == 1
        first_id = udp_payload(first['held'][0][1])[5:9]
        second_id = udp_payload(second['held'][0][1])[5:9]
        assert first_id != second_id

        # Deliver the stale response first. No later init can rescue this exchange.
        lab.release(first)
        lab.release(second)
        lab.wait(lambda: transport['hits'] == 1, 'a selected a session')
        selected = udp_payload(transport['held'][0][1])[1:5]
        assert selected == second_id, (
            f'a accepted the stale response: selected={selected.hex()}, '
            f'old={first_id.hex()}, current={second_id.hex()}')

        lab.wait_links('a', ['b'])
        lab.wait_links('b', ['a'])
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')
        stream = workload.SshServer(lab, 'b').stream('a')
        stream.progress()
        stream.finish()


lib.main(test)
