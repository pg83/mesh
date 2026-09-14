"""Force the real daemon to exercise source-port exhaustion and quarantine."""
import sys
import time

import lib


def quarantine(lab, proto):
    original_start = lab.start_node

    def start(name):
        if name == 'a':
            original_start('b')
            script = '''
import os, resource, socket, sys
hard = resource.getrlimit(resource.RLIMIT_NOFILE)[1]
limit = 4096 if hard == resource.RLIM_INFINITY else min(4096, hard)
resource.setrlimit(resource.RLIMIT_NOFILE, (limit, hard))
kind = socket.SOCK_DGRAM if sys.argv[1] == 'udp' else socket.SOCK_STREAM
ports = [p for p in range(49152, 65536) if p not in (50000, 50001)]
ready_r, ready_w = os.pipe()
life_r, life_w = os.pipe()
children = []
for offset in range(0, len(ports), limit-64):
    pid = os.fork()
    if pid == 0:
        os.close(ready_r)
        os.close(life_w)
        sockets = []
        for port in ports[offset:offset+limit-64]:
            s = socket.socket(socket.AF_INET, kind)
            s.bind(('10.1.0.1', port))
            sockets.append(s)
        os.write(ready_w, b'.')
        os.close(ready_w)
        os.read(life_r, 1)
        os._exit(0)
    children.append(pid)
os.close(ready_w)
os.close(life_r)
try:
    ready = b''
    while len(ready) < len(children):
        chunk = os.read(ready_r, len(children))
        assert chunk, 'port holder failed'
        ready += chunk
    print('ready', flush=True)
    sys.stdin.read()
finally:
    os.close(life_w)
    for child in children:
        os.waitpid(child, 0)
'''
            import subprocess
            holder = lab.spawn('a', [sys.executable, '-u', '-c', script, proto], 'port-holder', stdin=subprocess.PIPE)
            lab.wait(lambda: 'ready' in (lab.dir / 'port-holder.log').read_text(), 'occupied source ports', timeout=15)
            assert holder.poll() is None
        if lab.nodes[name].proc is None:
            original_start(name)

    lab.start_node = start
    with lab:
        lab.wait_ping('a', 'b')

        def source():
            state = lab.status('a')
            candidates = [state['addresses'][str(c['from'])] for c in state['channels']
                          if c['outgoing'] and not state['addresses'][str(c['from'])]['endpoint']]
            assert len(candidates) <= 1
            return candidates[0] if candidates else None

        first = source()
        assert first['port'] in (50000, 50001), first
        process = lab.nodes['a'].proc.pid

        def disconnect():
            lab.run('a', ['ip', 'addr', 'del', '10.1.0.1/24', 'dev', 's1'])
            lab.wait(lambda: source() is None, 'source socket closed')
            # The graph receives close before the socket-closing goroutine can
            # necessarily run. Observe the kernel too.
            path = '/proc/net/udp' if proto == 'udp' else '/proc/net/tcp'
            lab.wait(lambda: all(row.split()[9] == '0' or
                                int(row.split()[1].split(':')[1], 16) not in (50000, 50001)
                                for row in lab.run('a', ['cat', path]).stdout.splitlines()[1:]),
                     'kernel socket released')
            lab.run('a', ['ip', 'addr', 'add', '10.1.0.1/24', 'dev', 's1'])

        released = time.monotonic()
        disconnect()
        lab.wait(lambda: source() is not None, 'second free source port')
        second = source()
        assert second['port'] in (50000, 50001) and second != first
        lab.wait_ping('a', 'b')
        disconnect()
        time.sleep(3)
        assert source() is None, 'a quarantined source port was reused immediately'
        while time.monotonic() - released < 59:
            assert source() is None, 'source port reused before the one-minute quarantine'
            time.sleep(.5)
        lab.wait(lambda: source() is not None, 'quarantine expires and source port becomes reusable', timeout=15)
        assert source()['port'] in (50000, 50001)
        lab.wait_ping('a', 'b')
        assert lab.nodes['a'].proc.pid == process, 'test restarted the daemon and forgot the port registry'
