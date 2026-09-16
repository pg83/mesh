"""WSS is available with no listeners and with only a UDP listener."""
import lib
import ws
import work_load as workload


def test():
    for listen in [False, True]:
        lab = ws.TLSLab(proxy=True, bind='127.0.0.1')
        lab.statics = {'b'}
        lab.configs['a']['endpoint'] = [lib.endpoint('0.0.0.0', 7901)] if listen else []
        with lab:
            lab.wait_ping('a', 'b')
            lab.wait_ping('b', 'a')
            assert lab.endpoint_route('a', 'b')[0]['to']['proto'] == 'wss'
            for family in ['tcp', 'tcp6']:
                rows = lab.run('a', ['cat', '/proc/net/' + family]).stdout.splitlines()[1:]
                assert all(int(r.split()[1].split(':')[1], 16) == 8058 for r in rows if r.split()[3] == '0A')
            stream = workload.SshServer(lab, 'b').stream('a')
            stream.progress()
            lab.stop_node('b')
            lab.start_node('b')
            lab.wait_ping('a', 'b', timeout=20)
            stream.progress(timeout=20)
            stream.finish()


lib.main(test)
