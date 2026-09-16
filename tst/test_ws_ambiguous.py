"""Two public WebSocket endpoints sharing a bind, host and path cannot be told apart, so the upgrade is refused."""
import sys
import lib
import ws


def test():
    lab = ws.Lab(statics=[])
    lab.configs['b'] = dict(endpoint=[dict(proto='ws', addr='10.1.0.2', port=port, path='/mesh', bind_port=7100) for port in (80, 81)])
    with lab:
        def advertised():
            try:
                return any(v['proto'] == 'ws' and v['port'] == 81 for v in lab.status('b')['addresses'].values())
            except OSError:
                return False

        lab.wait(advertised, 'both endpoints advertised')
        code = '''
import http.client
for path in ['/mesh', '/other']:
    conn = http.client.HTTPConnection('10.1.0.2', 7100, timeout=10)
    conn.request('GET', path, headers={'Host': '10.1.0.2:7100', 'Upgrade': 'websocket', 'Connection': 'Upgrade',
                                        'Sec-WebSocket-Key': 'dGhlIHNhbXBsZSBub25jZQ==', 'Sec-WebSocket-Version': '13'})
    print(path, conn.getresponse().status)
'''
        # The listener answers once the next snapshot carries it.
        lab.wait(lambda: lab.run('a', [sys.executable, '-c', code]).stdout.split() == ['/mesh', '200', '/other', '404'], 'ambiguous upgrade refused')
        assert not lab.channels('b'), 'an ambiguous upgrade created channels'


lib.main(test)
