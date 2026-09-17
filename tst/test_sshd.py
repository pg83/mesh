"""The embedded SSH server answers on the mesh address only, authenticates ring keys and runs commands."""
import fcntl
import json
import os
import pty
import select
import struct
import subprocess
import termios
import time
import lib


class Ring(lib.Lab):
    def keygen(self, node):
        super().keygen(node)
        path = self.dir / f'{node.name}.ssh'
        subprocess.run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', path], check=True)
        self.run_args[node.name] = ['-key-file', str(path)]
        if node.name == 'b':
            self.run_args[node.name] += ['-sshd-port', '2222']
        if node.name == 'c':
            self.run_args[node.name] += ['-sshd', '-tun', '-sshd-authorized-keys', str(self.dir / 'authorized_keys')]
        if node.name == 'd':
            self.run_args[node.name] += ['-sshd']

    def registry(self):
        peers = super().registry()
        for peer, node in zip(peers, self.nodes.values()):
            peer['pub'] = (self.dir / f'{node.name}.ssh.pub').read_text().strip()
        return peers


def test():
    lab = Ring(['a', 'b', 'c', 'd'], {1: ['a', 'b', 'c', 'd']}, statics=['c'])
    # d runs where the system cannot name its own account: an empty passwd file
    # of its own, which is a host with a user database it cannot reach.
    # d runs on a host whose user database it cannot read: its own account is
    # not in there, and the one account that is has no groups to be found,
    # because there is no file of groups at all.
    lab.node_prefix['d'] = [
        'unshare', '-m', 'sh', '-c',
        'mount -t tmpfs tmpfs /etc && printf "weird:x:4242:4242::/tmp:/bin/sh\\n" > /etc/passwd && exec "$@"',
        'sh',
    ]
    # Without these the library names the account from the environment instead
    # of the database, and the database is the point.
    lab.node_env['d'] = dict(USER=None, HOME=None, SHELL=None, LOGNAME=None)
    lab.configs['b'] = dict(sshd=True)
    # b runs without HOME and SHELL: sessions fall back to / and the login shell of the account.
    lab.node_env['b'] = dict(HOME=None, SHELL=None)
    subprocess.run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', lab.dir / 'guest.ssh'], check=True)
    (lab.dir / 'authorized_keys').write_text('# guests\n\n' + (lab.dir / 'guest.ssh.pub').read_text())
    with lab:
        lab.wait_ping('a', 'b')
        lab.wait_ping('a', 'c')
        known = lab.dir / 'known_hosts'
        known.write_text(''.join(f'{lib.intip(lab.nodes[name].index)} {(lab.dir / f"{name}.ssh.pub").read_text()}' for name in 'bcd'))
        base = ['ssh', '-F', '/dev/null', '-o', 'BatchMode=yes', '-o', 'IdentitiesOnly=yes', '-o', 'StrictHostKeyChecking=yes',
                '-o', f'UserKnownHostsFile={known}', '-o', 'ConnectTimeout=10']

        def ssh(command, *options, key='a.ssh', port=2222, target='b', user='root', **kwargs):
            kwargs.setdefault('stdin', subprocess.DEVNULL)
            argv = base + ['-i', lab.dir / key, '-p', str(port), *options, user + '@' + lib.intip(lab.nodes[target].index)]
            return lab.run('a', argv + ([command] if command is not None else []), check=False, timeout=30, **kwargs)

        result = ssh('echo hello; id -u; echo $HOME')
        assert result.returncode == 0 and result.stdout == 'hello\n0\n/\n', result
        assert ssh('exit 7').returncode == 7
        # A command killed by a signal reports 255, like OpenSSH.
        assert ssh('kill -9 $$').returncode == 255
        # Without a command the session runs the login shell, named with the leading dash.
        result = ssh(None, '-tt', input='echo shell-$0; exit\n', stdin=None)
        assert result.returncode == 0 and 'shell--' in result.stdout, result
        # Only session channels exist: stdio forwarding is rejected by type.
        result = ssh(None, '-W', '127.0.0.1:1')
        assert result.returncode == 255 and 'only sessions are supported' in result.stderr, result
        resize(lab, base, lib.intip(lab.nodes['b'].index))
        result = ssh('cat; echo -n done >&2', input='piped input', stdin=None)
        assert result.stdout == 'piped input' and result.stderr.endswith('done'), result
        result = ssh('tty; echo $TERM', '-tt', env=dict(subprocess.os.environ, TERM='xterm-256color'))
        assert result.returncode == 0, result
        lines = result.stdout.replace('\r', '').split()
        assert lines[0].startswith('/dev/pts/') and lines[1] == 'xterm-256color', lines
        # A background child keeping the pty open does not hold the session past its exit.
        started = time.monotonic()
        result = ssh('sleep 20 >/dev/null 2>&1 & echo background', '-tt')
        assert result.returncode == 0 and 'background' in result.stdout and time.monotonic() - started < 10, result
        result = ssh('echo $MESH_TEST_ENV', '-o', 'SendEnv=MESH_TEST_ENV', env=dict(subprocess.os.environ, MESH_TEST_ENV='forwarded'))
        assert result.stdout == 'forwarded\n', result
        # The default port is 22 and the flag form accepts an extra authorized keys file.
        result = ssh('echo via-flag; echo $HOME', port=22, target='c')
        assert result.stdout == 'via-flag\n' + os.environ['HOME'] + '\n', result
        result = ssh('echo guest', key='guest.ssh', port=22, target='c')
        assert result.stdout == 'guest\n', result
        # The account of the node that cannot read a user database is its own
        # number, and a session there gets the shell of last resort.
        lab.wait(lambda: lab.route('a', 'd'), 'a has a route to d')
        result = ssh('echo $0', port=22, target='d', user='0')
        assert result.returncode == 0 and result.stdout.strip().endswith('sh'), result
        assert ssh('true', port=22, target='d').returncode == 255, 'root is not a name it knows'
        # An account the database describes in a way that cannot be used: the
        # session ends and the node goes on serving.
        assert ssh('true', port=22, target='d', user='weird').returncode == 255
        result = ssh('echo still-here', port=22, target='d', user='0')
        assert result.returncode == 0 and result.stdout.strip() == 'still-here', result
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
        # An SSH port outside 1..65535 stops the node at startup.
        lab.stop_node('c')
        config = json.loads((lab.dir / 'c.json').read_text())
        config.update(sshd=True, sshd_port=70000)
        (lab.dir / 'bad.json').write_text(json.dumps(config))
        result = lab.run('c', [lib.MESH, 'run', '-c', lab.dir / 'bad.json', '-key-file', lab.dir / 'c.ssh'], check=False, timeout=10)
        assert result.returncode != 0 and 'bad sshd port 70000' in result.stderr, result


def resize(lab, base, address):
    """A window-change request resizes the pty: the local terminal shrinks and the remote shell sees it."""
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 24, 80, 0, 0))
    script = 'echo started; for i in $(seq 100); do [ "$(stty size)" = "50 120" ] && echo resized && exit 0; sleep 0.1; done; stty size; exit 1'
    argv = lab.command('a', base + ['-i', lab.dir / 'a.ssh', '-p', '2222', '-tt', 'root@' + address, script])
    proc = subprocess.Popen(argv, stdin=slave, stdout=slave, stderr=slave,
                            preexec_fn=lambda: (os.setsid(), fcntl.ioctl(0, termios.TIOCSCTTY, 0)))
    os.close(slave)
    lab.processes.append(proc)

    def read_until(token, timeout=20):
        data = b''
        deadline = time.monotonic() + timeout
        while token not in data and time.monotonic() < deadline:
            if select.select([master], [], [], .2)[0]:
                try:
                    chunk = os.read(master, 4096)
                except OSError:
                    break
                if not chunk:
                    break
                data += chunk
        return data

    assert b'started' in read_until(b'started'), 'remote shell did not start'
    # The kernel sends SIGWINCH to the ssh client; it forwards the new size.
    fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack('HHHH', 50, 120, 0, 0))
    output = read_until(b'resized')
    assert proc.wait(timeout=20) == 0 and b'resized' in output, output
    os.close(master)


lib.main(test)
