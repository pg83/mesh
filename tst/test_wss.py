"""WSS verifies certificates; once trusted, a single TLS connection works both ways."""
import time
import lib
import ws
import workload


def test():
    lab = ws.TLSLab(trusted=False)
    lab.intercept('b', 'a', 'drop', syn=True, count=-1)
    with lab:
        time.sleep(3)
        assert not lab.connections('a') and not lab.connections('b'), 'untrusted certificate accepted'
        lab.stop_node('a')
        invalid_ca = lab.dir / 'invalid-ca.pem'
        invalid_ca.write_text('not a PEM certificate\n')
        registry = lab.registry()
        registry[1]['endpoint'][0]['tls_ca'] = str(invalid_ca)
        lab.configs['a']['registry'] = registry
        lab.start_node('a')
        time.sleep(3)
        assert not lab.connections('a') and not lab.connections('b'), 'invalid CA accepted'
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
