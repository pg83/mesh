"""Exit nodes carry traffic for other networks: the client routes them into the mesh, an exit forwards, flows survive an exit going away."""
import socket
import time
import lib


def metric(lab, name, key):
    return next(float(l.split()[1]) for l in lab.http(name, '/metrics')[2].decode().splitlines() if l.startswith(key + ' '))


def test():
    # a reaches "the internet", host i on segment 2, only through the exits e and f.
    lab = lib.Lab(['a', 'e', 'f', 'i'], {1: ['a', 'e', 'f'], 2: ['e', 'f', 'i']}, statics=['e', 'f'])
    lab.configs['a'] = dict(routes={'0.0.0.0/0': ['e', 'f']})
    lab.configs['e'] = dict(exit=True)
    lab.run_args['f'] = ['-exit']
    with lab:
        # i is a plain host on segment 2 once its node is gone.
        lab.wait(lambda: lab.run('i', [lib.MESH, 'status'], check=False).returncode == 0, 'i started')
        lab.stop_node('i')
        lab.run('i', ['ip', 'link', 'del', 'mesh0'])
        internet = lab.nodes['i'].addresses[2]
        for name in 'ef':
            lab.run(name, ['sh', '-c', 'echo 1 > /proc/sys/net/ipv4/ip_forward'])
        lab.run('i', ['ip', 'route', 'add', lib.SUBNET, 'via', lab.nodes['e'].addresses[2]])

        def return_path(exit):
            """The lab switch delivers by destination address: replies for a's mesh address on segment 2 go to the exit."""
            with lab.lock:
                lab.ports[(2, socket.inet_aton(lib.intip(1)))] = lab.ports[(2, socket.inet_aton(lab.nodes[exit].addresses[2]))]

        return_path('e')
        lab.wait_ping('a', 'e')
        lab.wait_ping('a', 'f')
        routes = lab.run('a', ['ip', 'route']).stdout
        assert '0.0.0.0/1 dev mesh0' in routes and '128.0.0.0/1 dev mesh0' in routes, routes
        assert all(r['exit'] for r in lab.status('a')['records'] if r['owner'] in (2, 3)), 'exits not advertised'

        def reaches():
            return lab.run('a', ['ping', '-c', '1', '-W', '1', internet], check=False).returncode == 0

        lab.wait(reaches, 'internet host reached through an exit')
        assert metric(lab, 'a', 'mesh_tun_exit_total') > 0
        # A TCP connection through the exit.
        lab.spawn('i', ['python3', '-c', 'import socket; s = socket.socket(); s.bind(("0.0.0.0", 9000)); s.listen(); c, _ = s.accept(); c.sendall(c.recv(64)); c.close()'], 'echo')
        time.sleep(.5)
        out = lab.run('a', ['python3', '-c', 'import socket; s = socket.create_connection(("%s", 9000), timeout=5); s.sendall(b"through-exit"); print(s.recv(64).decode())' % internet]).stdout
        assert out.strip() == 'through-exit', out
        # The client's own links to the exits keep flowing through the interface, not the mesh.
        assert lab.ping('a', 'e') and lab.ping('a', 'f')
        # One exit goes away: new flows use the other, the return route follows.
        lab.stop_node('e')
        lab.run('i', ['ip', 'route', 'replace', lib.SUBNET, 'via', lab.nodes['f'].addresses[2]])
        return_path('f')
        lab.wait(reaches, 'internet host reached through the remaining exit', timeout=20)
        # A route naming no live exit leaves packets unrouted.
        lab.stop_node('f')
        lab.wait(lambda: not reaches(), 'no exit left', timeout=20)


lib.main(test)
