"""Only one peer can initiate TCP; the accepted connection still carries both directions."""
import lib
import ws
import workload


def test():
    lab = ws.Lab(statics=['a'])
    blocked = lab.intercept('a', 'b', 'drop', syn=True, count=-1)
    with lab:
        lab.wait_ping('a', 'b')
        lab.wait(lab.one_connection, 'single incoming connection reused for reverse traffic')
        workload.udp_server(lab, 'b')
        client = workload.UdpClient(lab, 'a', 'b')
        client.send(b'accepted-socket-is-bidirectional')
        assert client.recv() == b'accepted-socket-is-bidirectional'
        assert lab.one_connection(), 'return traffic requires only the accepted connection'


lib.main(test)
