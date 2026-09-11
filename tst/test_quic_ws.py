"""The same SSH and QUIC connections migrate UDP -> WSS -> UDP without reconnecting."""
import lib
import ws
import workload


def test():
    with ws.TLSLab(mixed=True) as lab:
        lab.wait_ping('a', 'b')
        lab.wait(lambda: lab.endpoint_route('a', 'b')[0]['to']['proto'] == 'udp', 'UDP selected')
        ssh = workload.SshServer(lab, 'b').stream('a')
        server = workload.QuicServer(lab, 'b')
        client = server.client('a', seconds=30)
        client.start()
        client.progress()
        cuts = [lab.intercept(src, dst, 'drop', count=-1, target_port=7000)
                for src, dst in [('a', 'b'), ('b', 'a')]]
        lab.wait(lambda: lab.endpoint_route('a', 'b')[0]['to']['proto'] == 'wss', 'WSS selected')
        ssh.progress()
        client.progress()
        for cut in cuts:
            lab.clear(cut)
        lab.wait(lambda: lab.endpoint_route('a', 'b')[0]['to']['proto'] == 'udp', 'UDP restored')
        ssh.progress()
        client.progress()
        ssh.finish()
        server.finish([client])


lib.main(test)
