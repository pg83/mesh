"""IPv6-only physical links carry IPv4 mesh traffic and survive address changes."""
import lib
import workload


def test():
    with lib.Lab(['lab', 'laptop'], {1: ['lab', 'laptop']}, statics=['lab'], ipv6=[1]) as lab:
        lab.wait_ping('lab', 'laptop')
        lab.wait_ping('laptop', 'lab')
        for name in lab.nodes:
            addresses = lab.run(name, ['ip', '-4', '-o', 'addr', 'show', 'dev', 's1']).stdout
            assert not addresses, addresses
        stream = workload.SshServer(lab, 'laptop').stream('lab')
        lab.set_address('laptop', 1, '2001:db8:1::99')
        lab.wait(lambda: lab.selected_endpoint('lab', 'laptop') == '2001:db8:1::99:7000',
                 'IPv6 endpoint roaming', timeout=8)
        stream.progress()

        def attached():
            status = lab.status('laptop')
            return any(e['alive'] and e['from']['addr'] == '10.77.0.2'
                       and e['to']['addr'] == '2001:db8:1::99' for e in status['graph'])

        proc = lab.nodes['laptop'].proc
        # These deadlines are shorter than the 30s fallback scan: OS events must work.
        lab.run('laptop', ['ip', 'link', 'set', 's1', 'down'])
        lab.wait(lambda: not attached(), 'interface down notification', timeout=3)
        lab.run('laptop', ['ip', 'link', 'set', 's1', 'up'])
        lab.wait(attached, 'interface up notification', timeout=3)
        stream.progress()
        # Rapid interface disappearance while the scanner is reading addresses.
        for _ in range(12):
            lab.run('laptop', ['ip', 'link', 'add', 'temporary', 'type', 'dummy'])
            lab.run('laptop', ['ip', '-6', 'addr', 'add', '2001:db8:9::1/64', 'dev', 'temporary', 'nodad'])
            lab.run('laptop', ['ip', 'link', 'set', 'temporary', 'up'])
            lab.run('laptop', ['ip', 'link', 'del', 'temporary'])
        assert proc.poll() is None, 'mesh exited during interface removal'
        stream.progress()
        stream.finish()


lib.main(test)
