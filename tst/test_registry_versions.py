"""Config versions survive relay, reject rollback and rotate live peer keys."""
import copy

import lib
import work_load as workload


def entries(lab, name):
    try:
        return {p['index']: p for p in lab.status(name)['registry']}
    except OSError:
        return {}


def test():
    with lib.Lab(['a', 'b', 'c'], {1: ['a', 'b', 'c']}) as lab:
        lab.wait_ping('b', 'c')
        stream = workload.SshServer(lab, 'c').stream('b')
        original = copy.deepcopy(lab.registry())
        current = copy.deepcopy(original)
        current[2]['name'] = 'renamed'
        current.append(dict(name='offline', index=44, pub=original[0]['pub'],
                            intip=lib.intip(44), endpoint=[]))
        lab.stop_node('a')
        lab.configs['a'] = dict(registry=current, registry_version=20)
        lab.start_node('a')
        lab.wait(lambda: entries(lab, 'b').get(44, {}).get('version') == 20,
                 'new version and unknown entry propagated')
        lab.wait(lambda: entries(lab, 'c')[3]['name'] == 'renamed', 'own metadata updated')
        stream.progress()

        # Missing records are not deletions, and version 1 cannot undo version 20.
        lab.stop_node('a')
        lab.configs['a'] = dict(registry=original)
        lab.start_node('a')
        lab.wait(lambda: entries(lab, 'a').get(44, {}).get('version') == 20,
                 'restarted node recovers records from its peers')
        assert entries(lab, 'b')[3]['name'] == 'renamed'
        stream.finish()

        # Change c's actual private key, announce its new public key from a,
        # and make b refresh both discovery and established edge sessions.
        lab.stop_node('c')
        lab.keygen(lab.nodes['c'])
        current[2]['pub'] = lab.nodes['c'].keys['pub']
        lab.stop_node('a')
        lab.configs['a'] = dict(registry=current, registry_version=21)
        lab.configs['c'] = dict(registry=current, registry_version=21)
        lab.start_node('a')
        lab.start_node('c')
        lab.wait(lambda: entries(lab, 'b')[3]['pub'] == current[2]['pub'], 'new key installed')
        lab.wait_ping('b', 'c')
        lab.wait_ping('c', 'b')

        # Changing an endpoint and mesh IP updates routing without restarting b.
        lab.stop_node('c')
        lab.set_address('c', 1, '10.1.0.99')
        current[2]['endpoint'] = [lib.endpoint('10.1.0.99')]
        current[2]['intip'] = lib.intip(33)
        lab.stop_node('a')
        lab.configs['a'] = dict(registry=current, registry_version=22)
        lab.configs['c'] = dict(registry=current, registry_version=22)
        lab.start_node('a')
        lab.start_node('c')
        lab.wait(lambda: entries(lab, 'b')[3]['intip'] == lib.intip(33), 'new mesh address learned')
        lab.wait(lambda: lab.run('b', ['ping', '-c', '1', '-W', '1', lib.intip(33)],
                                check=False).returncode == 0, 'new mesh address reachable')


lib.main(test)
