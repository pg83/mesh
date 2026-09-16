"""A public WSS endpoint terminates on loopback; exported clients can bootstrap over it."""
import json
import os
import sys

import lib
import ws
import work_load as workload


def test():
    for bind in ['127.0.0.1', '::1']:
        lab = ws.TLSLab(proxy=True, bind=bind)
        lab.statics = {'b'}
        lab.intercept('b', 'a', 'drop', syn=True, count=-1)
        with lab:
            lab.wait_ping('a', 'b')
            lab.wait_ping('b', 'a')
            assert lab.endpoint_route('a', 'b')[0]['to']['proto'] == 'wss'
            # The origin port really listens only on loopback, not on the LAN.
            code = 'import socket; s=socket.socket(); s.settimeout(1); assert s.connect_ex(("10.1.0.2",7101)) != 0'
            lab.run('a', [sys.executable, '-c', code])
            for name in ['a', 'b']:
                assert not any(ep['addr'] in ['127.0.0.1', '::1'] for ep in lab.status(name)['addresses'].values())

            status, _, body = lab.http('b', '/config?node=a', 8058)
            assert status == 200
            config = json.loads(body)
            assert 'key' not in config
            assert config['endpoint'] == []
            assert b'bind_addr' not in body and b'tls_key' not in body
            lab.stop_node('a')
            lab.configs['a'] = config
            key = lab.dir / 'a.key'
            key.write_text(lab.nodes['a'].keys['key'])
            lab.run_args['a'] = ['-key-file', str(key)]
            # The local reverse proxy uses our test CA; the downloaded config stays intact.
            old_ca = os.environ.get('SSL_CERT_FILE')
            os.environ['SSL_CERT_FILE'] = str(lab.cert)
            try:
                lab.start_node('a')
            finally:
                if old_ca is None:
                    os.environ.pop('SSL_CERT_FILE')
                else:
                    os.environ['SSL_CERT_FILE'] = old_ca
            lab.wait_ping('a', 'b')
            assert lab.endpoint_route('a', 'b')[0]['to']['proto'] == 'wss'
            workload.udp_server(lab, 'b')
            client = workload.UdpClient(lab, 'a', 'b')
            client.send(b'exported-config-over-loopback-proxy')
            assert client.recv() == b'exported-config-over-loopback-proxy'


lib.main(test)
