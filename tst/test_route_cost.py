"""Routes follow link costs: UDP beats WebSocket, a direct WebSocket beats two UDP hops, the relay takes over when both are cut."""
import lib
import ws


def test():
    with ws.Lab(names=['a', 'r', 'b'], mixed=True) as lab:
        def transports(source, target):
            path = lab.endpoint_route(source, target)
            return path and [hop['to' if not hop['from']['endpoint'] else 'from']['proto'] for hop in path]

        def linked(source, proto):
            """source's graph has a link from one of its sockets into b's listener of that transport."""
            return any(e['from'].get('endpoint') is False and e['to'] == dict(proto=proto, addr='10.1.0.3', port=7000 if proto == 'udp' else 7100, endpoint=True, **({} if proto == 'udp' else dict(path='/mesh')))
                       for e in lab.status(source)['graph'])

        lab.wait_ping('a', 'b')
        lab.wait_ping('a', 'r')
        # Both a UDP and a WebSocket link into b exist; the UDP one is cheaper.
        lab.wait(lambda: linked('a', 'ws') and linked('a', 'udp'), 'UDP and WebSocket links into b')
        lab.wait(lambda: transports('a', 'b') == ['udp'], 'direct UDP route')
        # Without UDP to b, a direct WebSocket (6) ties two UDP hops via r (3 + 3) and wins on hops.
        rules = [lab.intercept(src, dst, 'drop', proto=17, target_port=7000, count=-1) for src, dst in [('a', 'b'), ('b', 'a')]]
        lab.wait(lambda: transports('a', 'b') == ['ws'], 'direct WebSocket route', timeout=20)
        assert lab.route('a', 'b') == ['b']
        # Without the WebSocket either, the relay carries the traffic over UDP.
        rules += [lab.intercept(src, dst, 'drop', proto=6, target_port=7100, count=-1) for src, dst in [('a', 'b'), ('b', 'a')]]
        rules += [lab.intercept(src, dst, 'drop', proto=6, source_port=7100, count=-1) for src, dst in [('a', 'b'), ('b', 'a')]]
        lab.wait(lambda: transports('a', 'b') == ['udp', 'udp'] and lab.route('a', 'b') == ['r', 'b'], 'relayed UDP route', timeout=20)
        lab.wait_ping('a', 'b')
        # Links back: the direct UDP route returns.
        for rule in rules:
            lab.clear(rule)
        lab.wait(lambda: transports('a', 'b') == ['udp'], 'direct UDP route restored', timeout=20)


lib.main(test)
