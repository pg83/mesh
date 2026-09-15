"""A WebSocket connection on a black-holed TCP path closes after five seconds of silence and is redialed."""
import time
import lib
import ws


def test():
    with ws.Lab() as lab:
        lab.wait_ping('a', 'b')
        lab.wait(lambda: lab.tcp_connections('a') >= 1, 'WebSocket established')
        # Drop every TCP segment both ways: the sockets stay open, nothing arrives.
        rules = [lab.intercept(src, dst, 'drop', proto=6, count=-1) for src, dst in [('a', 'b'), ('b', 'a')]]
        started = time.monotonic()
        lab.wait(lambda: not lab.channels('a') and not lab.channels('b'), 'dead connection closed on both sides', timeout=15)
        assert time.monotonic() - started < 12, 'closing waited for the kernel'
        assert 'link down' in (lab.dir / 'a.log').read_text()
        for rule in rules:
            lab.clear(rule)
        lab.wait(lambda: lab.channels('a') and lab.channels('b'), 'redialed once the path is back', timeout=15)
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')


lib.main(test)
