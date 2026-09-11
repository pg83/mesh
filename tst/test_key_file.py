"""SSH and seed files interoperate with inline keys over real mesh links."""

import base64
import copy
import json
import subprocess

import lib


class KeyFiles(lib.Lab):
    def keygen(self, node):
        super().keygen(node)
        if node.name == 'a':
            path = self.dir / 'home.key'
            subprocess.run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', path], check=True)
            self.run_args[node.name] = ['-key-file', str(path)]
        elif node.name == 'b':
            path = self.dir / 'seed with spaces.key'
            path.write_text(' \n' + node.keys['key'] + '\r\n ')
            path.chmod(0o600)
            self.run_args[node.name] = ['-key-file', str(path)]

    def registry(self):
        peers = super().registry()
        peers[0]['pub'] = (self.dir / 'home.key.pub').read_text().strip()
        peers[0].pop('sig')
        return peers

    def write_config(self, node):
        path = super().write_config(node)
        cfg = json.loads(path.read_text())
        if node.name == 'a':
            cfg.pop('key')
        elif node.name == 'b':
            cfg['key'] = 'ignored invalid inline key'
        path.write_text(json.dumps(cfg))
        return path


def test():
    with KeyFiles(['a', 'b', 'c'], {1: ['a', 'b', 'c']}, statics=['c']) as lab:
        for src in 'abc':
            for dst in 'abc':
                if src != dst:
                    lab.wait_ping(src, dst)
        for name in ('a', 'b'):
            lab.stop_node(name)
            lab.start_node(name)
            lab.wait_ping(name, 'c')
            lab.wait_ping('c', name)

        cfg = json.loads((lab.dir / 'a.json').read_text())
        def bad(keyfile, expected, change=lambda c: None):
            candidate = copy.deepcopy(cfg)
            change(candidate)
            path = lab.dir / 'invalid.json'
            path.write_text(json.dumps(candidate))
            args = [] if keyfile is None else ['-key-file', keyfile]
            result = lab.run('a', [lib.MESH, 'run', '-c', path, *args], timeout=10, check=False)
            assert result.returncode != 0
            assert expected in result.stderr, result.stderr
            assert lab.nodes['b'].keys['key'] not in result.stderr

        bad(None, 'bad key length')
        bad(lab.dir / 'absent', 'no such file', lambda c: c.update(key=lab.nodes['b'].keys['key']))
        empty = lab.dir / 'empty.key'
        empty.write_text(' \n')
        bad(empty, 'bad key length')
        invalid = lab.dir / 'invalid.key'
        invalid.write_text('not base64!')
        bad(invalid, 'base64')
        invalid.write_text('YQ==')
        bad(invalid, 'bad key length')
        invalid.write_text('-----BEGIN OPENSSH PRIVATE KEY-----\nbroken')
        bad(invalid, 'ssh:')
        bad(lab.dir / 'seed with spaces.key', 'private key does not match')

        encrypted = lab.dir / 'encrypted.key'
        subprocess.run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', 'test-password', '-f', encrypted], check=True)
        bad(encrypted, 'passphrase')
        rsa = lab.dir / 'rsa.key'
        subprocess.run(['ssh-keygen', '-q', '-t', 'rsa', '-b', '2048', '-N', '', '-f', rsa], check=True)
        bad(rsa, 'SSH private key must be Ed25519')

        home = lab.dir / 'home.key'
        public = (lab.dir / 'home.key.pub').read_text().strip()
        bad(home, 'no key found', lambda c: c['registry'][0].update(pub='ssh-ed25519 broken'))
        bad(home, 'expected one SSH public key', lambda c: c['registry'][0].update(pub=public + '\n' + public))
        bad(home, 'SSH public key must be Ed25519', lambda c: c['registry'][0].update(pub=(lab.dir / 'rsa.key.pub').read_text()))
        bad(home, 'signing key does not match SSH public key', lambda c: c['registry'][0].update(sig=lab.nodes['b'].keys['sig']))

        # A matching explicit signing key is also accepted for an SSH entry.
        sig = base64.b64encode(base64.b64decode(public.split()[1])[-32:]).decode()
        peers = lab.registry()
        peers[0]['sig'] = sig
        lab.configs.setdefault('a', {})['registry'] = peers
        lab.stop_node('a')
        lab.start_node('a')
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')


lib.main(test)
