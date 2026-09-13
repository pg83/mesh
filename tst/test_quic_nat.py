"""The same SSH and QUIC connections migrate LAN -> port forward -> second port forward."""
import lib
import nat
import workload


def test():
    with nat.Lab(lan=True) as lab:
        lab.wait_ping('a', 'b')
        # Initially only the ordinary LAN socket is reachable.
        cuts = lab.cut_port(7001) + lab.cut_port(7002)
        lab.wait(lambda: lab.selected_endpoint('a', 'b') == '10.1.0.2:7000', 'LAN route')
        ssh = workload.SshServer(lab, 'b').stream('a')
        server = workload.QuicServer(lab, 'b')
        client = server.client('a', seconds=30)
        client.start()
        client.progress()
        for rule in cuts:
            lab.clear(rule)
        lab.intercept('a', 'b', 'drop', count=-1, target_port=7000)
        lab.wait(lambda: lab.selected_endpoint('a', 'b') in ('198.51.100.2:17001', '198.51.100.2:17002'),
                 'public route after LAN failure')
        ssh.progress()
        client.progress()
        selected = int(lab.selected_endpoint('a', 'b').split(':')[1])
        lab.cut_port(selected - 10000)
        other = 17002 if selected == 17001 else 17001
        lab.wait(lambda: lab.selected_endpoint('a', 'b') == f'198.51.100.2:{other}',
                 'second public route after port failure')
        ssh.progress()
        client.progress()
        ssh.finish()
        server.finish([client])


lib.main(test)
