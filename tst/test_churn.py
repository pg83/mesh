"""Addresses appear and vanish under the daemon: sockets follow them, the links hold, the graph settles again."""
import time

import lib


def test():
    lab = lib.Lab(['a', 'b', 'c'], {1: ['a', 'b', 'c']}, statics=['a'])
    # b listens on the one address it has, so any other address it is given
    # needs a socket of its own.
    lab.configs['b'] = dict(endpoint=[lib.endpoint(lib.segaddr(1, 2))])
    with lab:
        pairs = [('a', 'b'), ('a', 'c'), ('b', 'c')]
        for src, dst in pairs:
            lab.wait_ping(src, dst)
        # Addresses come and go while the peers keep talking. Each one is a
        # socket to open, a vertex to publish and, a moment later, both to drop.
        for i in range(4):
            address = f'10.1.0.{200 + i}'
            lab.add_address('b', 1, address)
            lab.wait_ping('a', 'b')
            lab.run('b', ['ip', 'addr', 'del', lib.prefix(address), 'dev', 's1'])
            with lab.lock:
                lab.ports.pop((1, lib.ipbytes(address)), None)
        # An address can arrive when there is no port left to give it: one
        # ephemeral port remains and something else is sitting on it. The node
        # cannot open a socket for the address, so it never dials from it, and
        # the links it already has carry on.
        held = ('import socket, time\n'
                's = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)\n'
                's.bind(("0.0.0.0", 0))\n'
                'print(s.getsockname()[1], flush=True)\n'
                'time.sleep(600)\n')
        # Something takes a port of the kernel's own choosing, and the range the
        # kernel hands out then shrinks onto that one port.
        hog = lab.spawn('b', ['python3', '-c', held], 'hog')
        log = lab.dir / 'hog.log'
        lab.wait(lambda: log.read_text().split(), 'the port is taken')
        port = int(log.read_text().split()[0])
        lab.run('b', ['sh', '-c', f'echo "{port} {port}" > /proc/sys/net/ipv4/ip_local_port_range'])
        lab.add_address('b', 1, '10.1.0.220')
        # Several ticks of the node's clock pass with the address in place and
        # no socket behind it: every one of them looks for a source to dial
        # from and finds none.
        for _ in range(4):
            time.sleep(1)
            lab.wait_ping('a', 'b')
        hog.terminate()
        lab.run('b', ['sh', '-c', 'echo "32768 60999" > /proc/sys/net/ipv4/ip_local_port_range'])
        # A peer restarts in the middle of it, so dials land on a node that is
        # no longer the one they were started for.
        lab.stop_node('c')
        lab.add_address('b', 1, '10.1.0.210')
        lab.start_node('c')
        for address in ['10.1.0.210', '10.1.0.220']:
            lab.run('b', ['ip', 'addr', 'del', lib.prefix(address), 'dev', 's1'])
            with lab.lock:
                lab.ports.pop((1, lib.ipbytes(address)), None)
        for src, dst in pairs:
            lab.wait_ping(src, dst, timeout=30)
        # The addresses that went away are gone from what the node advertises.
        gone = {f'10.1.0.{200 + i}' for i in range(4)} | {'10.1.0.210', '10.1.0.220'}

        def advertised():
            return {v.get('addr') for r in lab.status('a')['records'] for v in r.get('vertices', [])}

        lab.wait(lambda: not gone & advertised(), 'the addresses that went away leave the graph', timeout=30)


lib.main(test)
