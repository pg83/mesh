"""The embedded SSH server answers on the mesh address only, authenticates ring keys and runs commands."""
import subprocess
import lib


class Ring(lib.Lab):
    def keygen(self, node):
        super().keygen(node)
        path = self.dir / f'{node.name}.ssh'
        subprocess.run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', path], check=True)
        self.run_args[node.name] = ['-key-file', str(path)]
        if node.name == 'c':
            self.run_args[node.name] += ['-sshd', '-sshd-authorized-keys', str(self.dir / 'guest.ssh.pub')]

    def registry(self):
        peers = super().registry()
        for peer, node in zip(peers, self.nodes.values()):
            peer['pub'] = (self.dir / f'{node.name}.ssh.pub').read_text().strip()
        return peers


def test():
    lab = Ring(['a', 'b', 'c'], {1: ['a', 'b', 'c']}, statics=['c'])
    lab.configs['b'] = dict(sshd=True, sshd_port=2222)
    subprocess.run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', lab.dir / 'guest.ssh'], check=True)
    with lab:
        lab.wait_ping('a', 'b')
        lab.wait_ping('a', 'c')
        known = lab.dir / 'known_hosts'
        known.write_text(''.join(f'{lib.intip(lab.nodes[name].index)} {(lab.dir / f"{name}.ssh.pub").read_text()}' for name in 'bc'))
        base = ['ssh', '-F', '/dev/null', '-o', 'BatchMode=yes', '-o', 'IdentitiesOnly=yes', '-o', 'StrictHostKeyChecking=yes',
                '-o', f'UserKnownHostsFile={known}', '-o', 'ConnectTimeout=10']

        def ssh(command, *options, key='a.ssh', port=2222, target='b', **kwargs):
            kwargs.setdefault('stdin', subprocess.DEVNULL)
            return lab.run('a', base + ['-i', lab.dir / key, '-p', str(port), *options, 'root@' + lib.intip(lab.nodes[target].index), command],
                           check=False, timeout=30, **kwargs)

        result = ssh('echo hello; id -u; echo $HOME')
        assert result.returncode == 0 and result.stdout == 'hello\n0\n' + subprocess.os.environ['HOME'] + '\n', result
        assert ssh('exit 7').returncode == 7
        result = ssh('cat; echo -n done >&2', input='piped input', stdin=None)
        assert result.stdout == 'piped input' and result.stderr.endswith('done'), result
        result = ssh('tty; echo $TERM', '-tt', env=dict(subprocess.os.environ, TERM='xterm-256color'))
        assert result.returncode == 0, result
        lines = result.stdout.replace('\r', '').split()
        assert lines[0].startswith('/dev/pts/') and lines[1] == 'xterm-256color', lines
        result = ssh('echo $MESH_TEST_ENV', '-o', 'SendEnv=MESH_TEST_ENV', env=dict(subprocess.os.environ, MESH_TEST_ENV='forwarded'))
        assert result.stdout == 'forwarded\n', result
        # The default port is 22 and the flag form accepts an extra authorized keys file.
        result = ssh('echo via-flag', port=22, target='c')
        assert result.stdout == 'via-flag\n', result
        result = ssh('echo guest', key='guest.ssh', port=22, target='c')
        assert result.stdout == 'guest\n', result
        # Keys outside the ring and unknown users are refused; the host key is the node key.
        assert ssh('true', key='guest.ssh').returncode == 255
        result = lab.run('a', base + ['-i', lab.dir / 'a.ssh', '-p', '2222', 'nosuchuser@' + lib.intip(2), 'true'], check=False, timeout=30)
        assert result.returncode == 255, result
        # A known account is accepted; this namespace cannot switch to it, so the command fails.
        result = lab.run('a', base + ['-i', lab.dir / 'a.ssh', '-p', '2222', 'nobody@' + lib.intip(2), 'true'], check=False, timeout=30)
        assert result.returncode == 255 and 'Permission denied' not in result.stderr, result
        wrong = lab.dir / 'wrong_hosts'
        wrong.write_text(f'{lib.intip(2)} {(lab.dir / "c.ssh.pub").read_text()}')
        strict = [option.replace(str(known), str(wrong)) for option in base]
        result = lab.run('a', strict + ['-i', lab.dir / 'a.ssh', '-p', '2222', 'root@' + lib.intip(2), 'true'], check=False, timeout=30)
        assert result.returncode == 255 and 'HOST KEY' in result.stderr.upper(), result
        # Nothing reached the kernel: the TUN delivered no TCP to port 2222 on b.
        assert lab.run('b', ['sh', '-c', 'cat /proc/net/tcp | grep -c ":08AE" || true']).stdout.strip() == '0'
        assert not any(link['idle'] > 5 for link in lab.status('b')['links'])
        lab.wait_ping('b', 'a')


lib.main(test)
