"""Failed WebSocket dials back off up to a minute; a new address or a link from the peer retries at once."""
import socket
import time
import lib
import ws


def failed(lab, name):
    try:
        lines = lab.http(name, '/metrics')[2].decode().splitlines()
    except OSError:
        return 0
    return next(float(l.split()[1]) for l in lines if l.startswith('mesh_dial_failed_total'))


class Lab(ws.Lab):
    def registry(self):
        registry = super().registry()
        # b advertises TLS on a listener that speaks plain WebSocket: every dial fails at once.
        registry[1]['endpoint'] = [dict(proto='wss', addr='10.1.0.2', port=7100, path='/mesh', bind_proto='ws')]
        return registry


def test():
    lab = Lab()
    lab.configs['b'] = dict(endpoint=[])
    # b cannot dial a's listener, so no link from b resets a's backoff until it is unblocked.
    blocked = lab.intercept('b', 'a', 'drop', proto=6, target_port=7100, count=-1)
    with lab:
        lab.wait(lambda: failed(lab, 'a') >= 1, 'first dial refused')
        before = failed(lab, 'a')
        time.sleep(30)
        assert failed(lab, 'a') - before <= 6, failed(lab, 'a') - before
        # A new interface address dials from it in the first second, whatever the backoff.
        before = failed(lab, 'a')
        lab.nsenter(lab.nodes['a'], 'ip', 'addr', 'add', '10.1.0.101/24', 'dev', 's1', check=True)
        with lab.lock:
            lab.ports[(1, socket.inet_aton('10.1.0.101'))] = lab.ports[(1, socket.inet_aton('10.1.0.1'))]
        lab.wait(lambda: failed(lab, 'a') > before, 'new address dialed at once', timeout=5)
        # Deep in backoff again, a link from b makes a retry within seconds; b is
        # restarted because its own dials backed off too.
        time.sleep(8)
        before = failed(lab, 'a')
        lab.clear(blocked)
        lab.stop_node('b')
        lab.start_node('b')
        lab.wait(lambda: any(l['from'].get('addr') == '10.1.0.2' for l in lab.status('a')['links']), 'b linked in', timeout=10)
        lab.wait(lambda: failed(lab, 'a') > before, 'retry after the peer linked in', timeout=4)


lib.main(test)
