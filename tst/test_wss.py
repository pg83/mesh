"""WSS verifies certificates; once trusted, a single TLS connection works both ways."""
import os
import time
import lib
import ws
import workload


def test():
    lab = ws.TLSLab(trusted=False)
    lab.intercept('b', 'a', 'drop', syn=True, count=-1)
    with lab:
        time.sleep(3)
        assert not lab.channels('a') and not lab.channels('b'), 'untrusted certificate accepted'
        lab.stop_node('a')
        invalid_ca = lab.dir / 'invalid-ca.pem'
        invalid_ca.write_text('not a PEM certificate\n')
        registry = lab.registry()
        registry[1]['endpoint'][0]['tls_ca'] = str(invalid_ca)
        lab.configs['a']['registry'] = registry
        # The server is trusted by the system pool here. Ignoring an invalid
        # explicit CA would therefore establish a connection and fail the test.
        previous = os.environ.get('SSL_CERT_FILE')
        os.environ['SSL_CERT_FILE'] = str(lab.cert)
        try:
            lab.start_node('a')
        finally:
            if previous is None:
                os.environ.pop('SSL_CERT_FILE')
            else:
                os.environ['SSL_CERT_FILE'] = previous
        time.sleep(3)
        assert not lab.channels('a') and not lab.channels('b'), 'invalid CA accepted'
        lab.stop_node('a')
        del lab.configs['a']['registry']
        lab.trusted = True
        lab.start_node('a')
        lab.wait_ping('a', 'b')
        lab.wait(lab.one_connection, 'single verified TLS connection')
        workload.udp_server(lab, 'b')
        client = workload.UdpClient(lab, 'a', 'b')
        client.send(b'verified-wss')
        assert client.recv() == b'verified-wss'


lib.main(test)
