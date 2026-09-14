"""The same SSH and QUIC connections migrate UDP -> WSS -> UDP without reconnecting."""
import lib
import ws
import workload


def test():
    lab = ws.TLSLab(mixed=True)
    ws_cuts = [lab.intercept(src, dst, 'drop', count=-1, target_port=7100)
               for src, dst in [('a', 'b'), ('b', 'a')]]
    with lab:
        lab.wait_ping('a', 'b')
        lab.wait(lambda: lab.endpoint_route('a', 'b')[0]['to']['proto'] == 'udp', 'UDP selected')
        ssh = workload.SshServer(lab, 'b').stream('a')
        server = workload.QuicServer(lab, 'b')
        client = server.client('a', seconds=30)
        client.start()
        client.progress()
        for cut in ws_cuts:
            lab.clear(cut)
        cuts = [lab.intercept(src, dst, 'drop', count=-1, target_port=7000)
                for src, dst in [('a', 'b'), ('b', 'a')]]
        lab.wait(lambda: any(edge['from']['proto'] in ('ws', 'wss') or edge['to']['proto'] in ('ws', 'wss')
                             for edge in lab.endpoint_route('a', 'b')), 'WS/WSS selected')
        ssh.progress()
        client.progress()
        for cut in cuts:
            lab.clear(cut)
        for src, dst in [('a', 'b'), ('b', 'a')]:
            lab.intercept(src, dst, 'drop', count=-1, target_port=7100)
            lab.intercept(src, dst, 'drop', count=-1, source_port=7100)
        lab.wait(lambda: lab.endpoint_route('a', 'b')[0]['to']['proto'] == 'udp', 'UDP restored')
        ssh.progress()
        client.progress()
        ssh.finish()
        server.finish([client])


lib.main(test)
