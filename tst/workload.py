"""Real application processes used by the network scenarios."""

import hashlib
import json
import os
import pwd
import select
import shlex
import shutil
import subprocess
import sys
import threading
import time
from pathlib import Path

import lib

PROGRAM = Path(__file__).with_name('program.py')


def require(*names):
    for name in names:
        assert shutil.which(name), f'required test program missing: {name}'


def address(lab, name):
    return lib.intip(lab.nodes[name].index)


def sha(path):
    with open(path, 'rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def random_file(path, size=4 << 20):
    with open(path, 'wb') as stream:
        for offset in range(0, size, 65536):
            stream.write(os.urandom(min(65536, size - offset)))
    return sha(path)


def wait_port(lab, name, host, port, daemon):
    code = 'import socket; socket.create_connection((%r, %d), .3).close()' % (host, port)
    def ready():
        assert daemon.poll() is None, f'server exited: {daemon.args}'
        return lab.run(name, [sys.executable, '-c', code], check=False).returncode == 0
    lab.wait(ready, f'{host}:{port} listening', timeout=15)


class SshServer:
    def __init__(self, lab, name):
        require('ssh', 'sshd', 'ssh-keygen', 'scp')
        self.lab, self.name = lab, name
        directory = lab.dir / ('ssh-' + name)
        directory.mkdir()
        for key in ('host', 'user'):
            lab.run(name, ['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', directory / key], user=True)
        config = directory / 'sshd_config'
        self.port = 2222
        self.host = address(lab, name)
        config.write_text(
            f'Port {self.port}\nListenAddress {self.host}\nHostKey {directory}/host\n'
            'PidFile none\nUsePAM no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\n'
            f'AuthorizedKeysFile {directory}/user.pub\nStrictModes no\nPubkeyAuthentication yes\n'
            'Subsystem sftp internal-sftp\nLogLevel VERBOSE\n'
            f'SetEnv PATH={os.environ["PATH"]}\n'
        )
        self.daemon = lab.spawn(name, [shutil.which('sshd'), '-D', '-e', '-f', config], 'sshd-' + name, user=True)
        wait_port(lab, name, self.host, self.port, self.daemon)
        user = pwd.getpwuid(int(os.environ['MESH_TEST_UID'])).pw_name
        self.target = f'{user}@{self.host}'
        self.options = ['-i', str(directory / 'user'), '-F', '/dev/null',
                        '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=no',
                        '-o', 'UserKnownHostsFile=/dev/null', '-o', 'ConnectTimeout=10',
                        '-o', 'ControlMaster=no', '-o', 'ControlPath=none',
                        '-o', 'ServerAliveInterval=5', '-o', 'ServerAliveCountMax=24']
        self.ssh = ['ssh', *self.options, '-p', str(self.port)]
        self.scp = ['scp', *self.options, '-P', str(self.port)]

    def command(self, source, command):
        return self.lab.run(source, [*self.ssh, self.target, command], user=True).stdout

    def stream(self, source):
        return SshStream(self, source)


class SshStream:
    """One SSH, one remote process, a new numbered request after each reply."""

    def __init__(self, server, source):
        self.lab = server.lab
        self.source = source
        self.proc = self.lab.spawn(source,
            [*server.ssh, server.target, shlex.join([sys.executable, str(PROGRAM), 'stream'])],
            'ssh-stream-' + source, user=True, stdin=subprocess.PIPE, stdout=subprocess.PIPE)
        self.condition = threading.Condition()
        self.stopping = threading.Event()
        self.replies = 0
        self.max_gap = 0
        self.error = None
        self.identity = None
        self.worker = threading.Thread(target=self.exchange, daemon=True)
        self.worker.start()
        self.progress(timeout=15)
        assert self.identity['connection'].split()[0] == address(self.lab, source), self.identity

    def exchange(self):
        try:
            self.identity = json.loads(self.proc.stdout.readline())
            last = time.monotonic()
            while not self.stopping.is_set():
                payload = f'{self.source}:{self.replies}\n'.encode()
                self.proc.stdin.write(payload)
                self.proc.stdin.flush()
                reply = self.proc.stdout.readline()
                assert reply == payload, (payload, reply)
                now = time.monotonic()
                with self.condition:
                    self.max_gap = max(self.max_gap, now - last)
                    self.replies += 1
                    self.condition.notify_all()
                last = now
                self.stopping.wait(.1)
            self.proc.stdin.close()
            assert self.proc.stdout.read() == b'', 'unsolicited or duplicate SSH output'
            assert self.proc.wait(timeout=10) == 0
        except BaseException as error:
            with self.condition:
                self.error = error
                self.condition.notify_all()

    def progress(self, after=None, timeout=60):
        with self.condition:
            target = self.replies if after is None else after
            deadline = time.monotonic() + timeout
            while self.replies <= target:
                if self.error:
                    raise AssertionError('SSH exchange failed') from self.error
                assert self.proc.poll() is None, f'SSH exited: {self.proc.returncode}'
                remaining = deadline - time.monotonic()
                assert remaining > 0, f'SSH stalled for {timeout}s'
                self.condition.wait(min(remaining, .2))
        self.lab.check()

    def finish(self):
        self.stopping.set()
        self.worker.join(timeout=65)
        assert not self.worker.is_alive(), 'SSH did not finish'
        if self.error:
            raise AssertionError('SSH exchange failed') from self.error
        assert self.max_gap < 60, f'SSH pause too long: {self.max_gap}'
        print(f'SSH {self.source}: {self.replies} replies, max pause {self.max_gap:.3f}s, {self.identity}', flush=True)


class UdpClient:
    def __init__(self, lab, source, target, port=9000):
        self.proc = lab.spawn(source, [sys.executable, PROGRAM, 'udp-client', address(lab, target), port],
                              'udp-client-' + source, stdin=subprocess.PIPE, stdout=subprocess.PIPE)

    def request(self, request):
        self.proc.stdin.write(json.dumps(request).encode() + b'\n')
        self.proc.stdin.flush()
        ready, _, _ = select.select([self.proc.stdout], [], [], max(10, request.get('recv', 0) + 5))
        assert ready, 'UDP application did not answer'
        return json.loads(self.proc.stdout.readline())

    def send(self, data):
        assert self.request({'send': data.hex()}) is True

    def recv(self, timeout=2):
        data = self.request({'recv': timeout})
        return bytes.fromhex(data) if data is not None else None


def udp_server(lab, name, port=9000):
    proc = lab.spawn(name, [sys.executable, PROGRAM, 'udp-server', address(lab, name), port], 'udp-server-' + name)
    log = lab.dir / ('udp-server-' + name + '.log')
    def ready():
        assert proc.poll() is None, 'UDP server exited'
        return log.exists() and log.read_text().startswith('ready\n')
    lab.wait(ready, 'UDP server ready')
    return log
