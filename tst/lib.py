"""E2E lab: mesh nodes in separate network namespaces, wired by a userspace
switch.

The kernel here has no veth, so every node gets one TUN per segment as its
"physical" interface and this process copies IP packets between TUNs of the
same segment. A segment is a LAN: nodes on it see each other directly,
nothing else. Requires unprivileged user namespaces; a test re-execs itself
under `unshare -rUn` and spawns one `unshare -n` holder per node.
"""

import collections
import fcntl
import heapq
import json
import os
import shutil
import signal
import select
import socket
import struct
import subprocess
import sys
import tempfile
import threading
import time
import uuid
from pathlib import Path

MESH = Path(os.environ["MESH_TEST_BINARY"]).resolve()
PORT = 7000
SUBNET = "10.77.0.0/24"
STATUS = "@mesh"  # abstract socket: per netns, and no path length limit

LIFETIME = "600"  # seconds; bounds what a killed lab can leak

IFF_TUN = 0x0001
IFF_NO_PI = 0x1000
TUNSETIFF = 0x400454CA


def unshared():
    """Re-exec under a fresh user+net namespace once."""
    if os.environ.get("MESH_TEST_UNSHARED") == "1":
        return
    env = dict(os.environ, MESH_TEST_UNSHARED="1",
               MESH_TEST_UID=str(os.getuid()), MESH_TEST_GID=str(os.getgid()))
    os.execvpe("unshare", ["unshare", "-rUn", sys.executable, *sys.argv], env)


def intip(index):
    return f"10.77.0.{index}"


def endpoint(address, port=PORT):
    return dict(ip=int.from_bytes(socket.inet_aton(address), 'little'), port=port)


def endpoint_address(ep):
    return socket.inet_ntoa(ep['ip'].to_bytes(4, 'little'))


def edge(source, target, ident, alive=True, ttl=5000):
    return dict(**{'from': source, 'to': target}, id=ident, alive=alive, ttl=ttl)


def segaddr(seg, index):
    return f"10.{seg}.0.{index}"


class Node:
    def __init__(self, name, index):
        self.name = name
        self.index = index
        self.segments = []
        self.holder = None
        self.pid = None
        self.proc = None
        self.keys = None
        self.addresses = {}
        self.coverage = None


class Lab:
    def __init__(self, nodes, segments, statics=None):
        """nodes: names, index is position + 1. segments: {seg: [names]}.
        statics: names that publish their segment addresses; default all."""
        self.nodes = {n: Node(n, i + 1) for i, n in enumerate(nodes)}
        self.segments = segments
        self.statics = set(nodes if statics is None else statics)
        self.dir = Path(tempfile.mkdtemp(prefix="mesh-lab-"))
        self.tuns = {}  # fd -> (seg, node)
        self.ports = {}  # (seg, addr bytes) -> fd
        self.running = True
        self.lock = threading.RLock()
        self.rules = []
        self.blocked = set()
        self.counts = collections.Counter()
        self.delayed = []
        self.serial = 0
        self.switch_error = None
        self.thread = None
        self.processes = []
        self.configs = {}
        self.coverage_dirs = []
        for seg, names in segments.items():
            for name in names:
                self.nodes[name].segments.append(seg)

    # --- namespaces and wires ---

    def netns(self, pid):
        return f"/proc/{pid}/ns/net"

    def nsenter(self, node, *cmd, **kw):
        kw.setdefault("timeout", 10)
        return subprocess.run(
            ["nsenter", "-t", str(node.pid), "-n", *cmd], **kw
        )

    def holder_pid(self, node):
        """The netns owner is the sleep under timeout+unshare, not the wrapper."""
        pid = node.holder.pid
        while True:
            children = subprocess.run(["pgrep", "-P", str(pid)], stdout=subprocess.PIPE, text=True).stdout.split()
            if not children:
                return pid
            pid = int(children[0])

    def start_holder(self, node):
        mine = os.stat("/proc/self/ns/net").st_ino
        node.holder = subprocess.Popen(
            ["timeout", LIFETIME, "unshare", "-n", "sleep", "infinity"],
            stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        deadline = time.monotonic() + 10
        while os.stat(self.netns(self.holder_pid(node))).st_ino == mine:
            assert node.holder.poll() is None, "namespace holder exited"
            assert time.monotonic() < deadline, "namespace holder did not start"
            time.sleep(0.01)
        node.pid = self.holder_pid(node)
        self.nsenter(node, "ip", "link", "set", "lo", "up", check=True)

    def add_wire(self, node, seg):
        name = f"s{seg}"
        nsfd = os.open(self.netns(node.pid), os.O_RDONLY)
        mine = os.open("/proc/self/ns/net", os.O_RDONLY)
        os.setns(nsfd, os.CLONE_NEWNET)
        fd = os.open("/dev/net/tun", os.O_RDWR)
        fcntl.ioctl(fd, TUNSETIFF, struct.pack("16sH", name.encode(), IFF_TUN | IFF_NO_PI))
        os.setns(mine, os.CLONE_NEWNET)
        os.close(nsfd)
        os.close(mine)
        addr = segaddr(seg, node.index)
        node.addresses[seg] = addr
        self.nsenter(node, "ip", "addr", "add", f"{addr}/24", "dev", name, check=True)
        self.nsenter(node, "ip", "link", "set", name, "up", check=True)
        self.tuns[fd] = (seg, node)
        self.ports[(seg, socket.inet_aton(addr))] = fd

    def block(self, src, dst, seg=None, both=True):
        with self.lock:
            self.blocked.add((src, dst, seg))
            if both:
                self.blocked.add((dst, src, seg))

    def unblock(self, src, dst, seg=None, both=True):
        with self.lock:
            self.blocked.discard((src, dst, seg))
            if both:
                self.blocked.discard((dst, src, seg))

    def intercept(self, src, dst, action, count=1, kind=None, seg=None,
                  min_size=0, max_size=None, every=1, delay=0, rate=None, source_ip=None, target_ip=None):
        rule = dict(src=src, dst=dst, action=action, count=count, kind=kind,
                    seg=seg, min_size=min_size, max_size=max_size, every=every, delay=delay,
                    rate=rate, source_ip=source_ip, target_ip=target_ip, next=0, seen=0, hits=0, held=[])
        with self.lock:
            self.rules.append(rule)
        return rule

    def clear(self, rule):
        with self.lock:
            self.rules.remove(rule)

    def release(self, rule, reverse=False):
        with self.lock:
            held, rule['held'] = rule['held'], []
            for out, packet, key in reversed(held) if reverse else held:
                self.deliver(out, packet, key)

    def replay(self, rule, src=None, dst=None, seg=None, transform=None):
        """Reinject captured UDP packets, optionally on another channel or with changed bytes."""
        with self.lock:
            assert rule['held'], 'no captured packet to replay'
            for _, original, key in rule['held']:
                source, target, channel = src or key[0], dst or key[1], seg or key[2]
                packet = bytearray(original)
                head = (packet[0] & 15) * 4
                assert packet[9] == 17 and not (struct.unpack_from('!H', packet, 6)[0] & 0x3fff)
                if transform:
                    packet[head + 8:] = transform(bytes(packet[head + 8:]))
                packet[12:16] = socket.inet_aton(self.nodes[source].addresses[channel])
                packet[16:20] = socket.inet_aton(self.nodes[target].addresses[channel])
                struct.pack_into('!H', packet, 2, len(packet))
                struct.pack_into('!H', packet, head + 4, len(packet) - head)
                packet[head + 6:head + 8] = b'\0\0'
                packet[10:12] = b'\0\0'
                checksum = sum(struct.unpack('!' + 'H' * (head // 2), packet[:head]))
                while checksum >> 16:
                    checksum = (checksum & 0xffff) + (checksum >> 16)
                struct.pack_into('!H', packet, 10, ~checksum & 0xffff)
                out = self.ports[(channel, bytes(packet[16:20]))]
                self.deliver(out, bytes(packet), (source, target, channel))

    def traffic(self, src, dst, seg=None):
        with self.lock:
            return sum(v for (a, b, s, action), v in self.counts.items()
                       if a == src and b == dst and action == 'sent' and (seg is None or seg == s))

    def deliver(self, out, packet, key):
        os.write(out, packet)
        self.counts[(*key, 'sent')] += 1

    def switch(self):
        try:
            fds = list(self.tuns)
            while self.running:
                ready, _, _ = select.select(fds, [], [], 0.01)
                with self.lock:
                    now = time.monotonic()
                    while self.delayed and self.delayed[0][0] <= now:
                        _, _, out, packet, key = heapq.heappop(self.delayed)
                        self.deliver(out, packet, key)
                    for fd in ready:
                        packet = os.read(fd, 65536)
                        if len(packet) < 20 or packet[0] >> 4 != 4:
                            continue
                        seg, src = self.tuns[fd]
                        out = self.ports.get((seg, packet[16:20]))
                        if out is None or out == fd:
                            continue
                        dst = self.tuns[out][1]
                        key = (src.name, dst.name, seg)
                        if key in self.blocked or (src.name, dst.name, None) in self.blocked:
                            self.counts[(*key, 'dropped')] += 1
                            continue
                        head = (packet[0] & 15) * 4
                        payload = packet[head + 8:] if packet[9] == 17 else b''
                        for rule in self.rules:
                            if (rule['src'] != src.name or rule['dst'] != dst.name
                                    or (rule['source_ip'] is not None and packet[12:16] != socket.inet_aton(rule['source_ip']))
                                    or (rule['target_ip'] is not None and packet[16:20] != socket.inet_aton(rule['target_ip']))
                                    or rule['count'] == 0
                                    or rule['seg'] not in (None, seg)
                                    or len(payload) < rule['min_size']
                                    or (rule['max_size'] is not None and len(payload) > rule['max_size'])
                                    or (rule['kind'] is not None and payload[:1] != bytes([rule['kind']]))):
                                continue
                            rule['seen'] += 1
                            if rule['seen'] % rule['every']:
                                continue
                            rule['hits'] += 1
                            if rule['count'] > 0:
                                rule['count'] -= 1
                            action = rule['action']
                            self.counts[(*key, action)] += 1
                            if action in ('hold', 'copy'):
                                rule['held'].append((out, packet, key))
                            if action in ('hold', 'drop'):
                                break
                            if action == 'delay':
                                self.serial += 1
                                heapq.heappush(self.delayed, (now + rule['delay'], self.serial, out, packet, key))
                                break
                            if action == 'pace':
                                if rule['next'] > now + .2:
                                    self.counts[(*key, 'queue-full')] += 1
                                    break
                                rule['next'] = max(now, rule['next']) + len(packet) / rule['rate']
                                self.serial += 1
                                heapq.heappush(self.delayed, (rule['next'], self.serial, out, packet, key))
                                break
                            if action == 'corrupt':
                                # IPv4 permits a zero UDP checksum. Keep the IP header
                                # intact so the altered ciphertext reaches mesh's AEAD.
                                damaged = bytearray(packet)
                                damaged[head + 6:head + 8] = b'\0\0'
                                damaged[-1] ^= 1
                                packet = bytes(damaged)
                            if action == 'duplicate':
                                self.deliver(out, packet, key)
                            self.deliver(out, packet, key)
                            break
                        else:
                            self.deliver(out, packet, key)
        except BaseException as error:
            self.switch_error = error

    def set_address(self, name, seg, address):
        node = self.nodes[name]
        with self.lock:
            old = node.addresses[seg]
            fd = self.ports.pop((seg, socket.inet_aton(old)))
            self.nsenter(node, 'ip', 'addr', 'del', f'{old}/24', 'dev', f's{seg}', check=True)
            self.nsenter(node, 'ip', 'addr', 'add', f'{address}/24', 'dev', f's{seg}', check=True)
            self.ports[(seg, socket.inet_aton(address))] = fd
            node.addresses[seg] = address

    def command(self, name, argv, user=False):
        command = ['nsenter', '-t', str(self.nodes[name].pid), '-n']
        if user:
            command += ['unshare', '-U', '--map-user=' + os.environ['MESH_TEST_UID'],
                        '--map-group=' + os.environ['MESH_TEST_GID']]
        return command + [str(a) for a in argv]

    def run(self, name, argv, user=False, **kwargs):
        kwargs.setdefault('check', True)
        kwargs.setdefault('timeout', 90)
        kwargs.setdefault('capture_output', True)
        kwargs.setdefault('text', True)
        return subprocess.run(self.command(name, argv, user), **kwargs)

    def spawn(self, name, argv, label, user=False, **kwargs):
        with open(self.dir / f'{label}.log', 'ab') as log:
            kwargs.setdefault('stdout', log)
            kwargs.setdefault('stderr', log)
            kwargs.setdefault('stdin', subprocess.DEVNULL)
            proc = subprocess.Popen(['timeout', '--preserve-status', LIFETIME, *self.command(name, argv, user)],
                                    start_new_session=True, **kwargs)
        self.processes.append(proc)
        return proc

    def check(self):
        if self.switch_error:
            raise AssertionError('userspace switch failed') from self.switch_error
        for node in self.nodes.values():
            if node.proc is not None:
                assert node.proc.poll() is None, f'mesh {node.name} exited: {node.proc.returncode}'

    def wait(self, predicate, description, timeout=30):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            self.check()
            if predicate():
                return
            time.sleep(0.05)
        raise AssertionError(f'timed out after {timeout}s: {description}')

    # --- nodes ---

    def keygen(self, node):
        out = subprocess.run([MESH, "keygen"], stdout=subprocess.PIPE, check=True, text=True)
        node.keys = json.loads(out.stdout)

    def registry(self):
        reg = []
        for node in self.nodes.values():
            static = []
            if node.name in self.statics:
                static = [f"{node.addresses[seg]}:{PORT}" for seg in node.segments]
            reg.append({
                "index": node.index,
                "pub": node.keys["pub"],
                "sig": node.keys["sig"],
                "intip": intip(node.index),
                "static": static,
            })
        return reg

    def write_config(self, node):
        cfg = {
            "index": node.index,
            "key": node.keys["key"],
            "port": PORT,
            "subnet": SUBNET,
            "status": STATUS,
            "registry": self.registry(),
        }
        cfg.update(self.configs.get(node.name, {}))
        path = self.dir / f"{node.name}.json"
        path.write_text(json.dumps(cfg, indent=2))
        return path

    def start_node(self, name):
        node = self.nodes[name]
        assert node.proc is None
        env = os.environ.copy()
        if env.get('GOCOVERDIR'):
            node.coverage = Path(env['GOCOVERDIR']) / ('daemon-' + name + '-' + uuid.uuid4().hex)
            node.coverage.mkdir(parents=True)
            self.coverage_dirs.append(node.coverage)
            env['GOCOVERDIR'] = str(node.coverage)
        node.proc = self.spawn(name, [MESH, 'run', '-c', self.write_config(node)], name, env=env)

    def stop_node(self, name):
        node = self.nodes[name]
        proc = node.proc
        assert proc is not None
        proc.terminate()
        code = proc.wait(timeout=10)
        node.proc = None
        assert code == 0, f'mesh {name} exited with {code}'
        if node.coverage:
            assert list(node.coverage.glob('covcounters.*')), f'no daemon counters in {node.coverage}'

    def start(self):
        for node in self.nodes.values():
            self.keygen(node)
            self.start_holder(node)
            for seg in node.segments:
                self.add_wire(node, seg)
        self.thread = threading.Thread(target=self.switch, daemon=True)
        self.thread.start()
        for name in self.nodes:
            self.start_node(name)

    def stop(self):
        errors = []
        for node in self.nodes.values():
            if node.proc:
                try:
                    self.stop_node(node.name)
                except Exception as error:
                    errors.append(error)
        for proc in self.processes:
            if proc.poll() is None:
                os.killpg(proc.pid, signal.SIGTERM)
        for proc in self.processes:
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(proc.pid, signal.SIGKILL)
                proc.wait()
                errors.append(AssertionError(f'process did not stop: {proc.args}'))
        self.running = False
        if self.thread:
            self.thread.join(timeout=5)
            assert not self.thread.is_alive(), 'switch did not stop'
        for fd in self.tuns:
            os.close(fd)
        for node in self.nodes.values():
            if node.holder:
                node.holder.terminate()
                node.holder.wait(timeout=5)
        if errors:
            raise errors[0]
        if self.switch_error:
            raise AssertionError('userspace switch failed') from self.switch_error

    def __enter__(self):
        try:
            self.start()
        except BaseException:
            self.dump_logs()
            self.stop()
            raise
        return self

    def __exit__(self, kind, value, tb):
        if kind is not None:
            for name in self.nodes:
                try:
                    (self.dir / f'{name}-udp.txt').write_text(
                        self.run(name, ['cat', '/proc/net/snmp', '/proc/net/udp', '/proc/net/dev']).stdout)
                    (self.dir / f'{name}-status.json').write_text(json.dumps(self.status(name), indent=2))
                except Exception:
                    pass
        try:
            self.stop()
        except BaseException:
            self.dump_logs()
            raise
        if kind is not None:
            self.dump_logs()
        else:
            shutil.rmtree(self.dir)

    def dump_logs(self):
        sys.stderr.write(f'lab artifacts: {self.dir}\n')
        for path in self.dir.glob('*.log'):
            sys.stderr.write(f'--- {path.name} ---\n{path.read_text(errors="replace")}')
        counters = {str(k): v for k, v in self.counts.items()}
        (self.dir / 'channels.json').write_text(json.dumps(counters, indent=2))
        sys.stderr.write(f'link counters: {counters}\n')
        artifacts = os.environ.get('MESH_TEST_ARTIFACTS')
        if artifacts:
            def special(directory, names):
                return [n for n in names if not (Path(directory, n).is_file() or Path(directory, n).is_dir())]
            shutil.copytree(self.dir, Path(artifacts) / self.dir.name, dirs_exist_ok=True, ignore=special)

    # --- observations ---

    def status(self, name):
        """Asks the node itself, from inside its namespace."""
        self.check()
        node = self.nodes[name]
        r = self.nsenter(node, MESH, "status", "-s", STATUS,
                         stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        if r.returncode != 0:
            raise OSError(f"{name}: status failed")
        return json.loads(r.stdout)

    def links(self, name):
        by_address = {address:node.index for node in self.nodes.values() for address in node.addresses.values()}
        return {by_address[endpoint_address(link['from'])] for link in self.status(name)['links']
                if endpoint_address(link['from']) in by_address}

    def wait_links(self, name, peers, timeout=15):
        want = {self.nodes[p].index for p in peers}
        deadline = time.time() + timeout
        last = None
        while time.time() < deadline:
            try:
                last = self.links(name)
                if last == want:
                    return
            except (OSError, json.JSONDecodeError) as error:
                last = error
            time.sleep(0.2)
        raise AssertionError(f"{name}: links {last!r} != {want} after {timeout}s")

    def ping(self, src, dst):
        node = self.nodes[src]
        r = self.nsenter(node, "ping", "-c", "1", "-W", "1", intip(self.nodes[dst].index),
                         stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        return r.returncode == 0

    def wait_ping(self, src, dst, timeout=10):
        deadline = time.time() + timeout
        while time.time() < deadline:
            if self.ping(src, dst):
                return
        raise AssertionError(f"ping {src} -> {dst} failed for {timeout}s")

    def route(self, src, dst):
        """The hop list src currently uses towards dst, node names."""
        by_address = {address:name for name,node in self.nodes.items() for address in node.addresses.values()}
        path = self.endpoint_route(src, dst)
        return [by_address[endpoint_address(hop['to'])] for hop in path] if path else None

    def endpoint_route(self, src, dst):
        return self.status(src)['routes'].get(intip(self.nodes[dst].index) + ':0')

    def selected_endpoint(self, src, dst):
        path = self.endpoint_route(src, dst)
        return endpoint_address(path[0]['to']) + ':' + str(path[0]['to']['port']) if path else None

    def known_nodes(self, name):
        vertices = self.status(name)['vertices']
        return sorted(node.index for node in self.nodes.values() if endpoint(intip(node.index), 0) in vertices)

    def wait_route(self, src, dst, hops, timeout=30):
        deadline = time.time() + timeout
        while time.time() < deadline:
            try:
                if self.route(src, dst) == hops:
                    return
            except (OSError, json.JSONDecodeError):
                pass
            time.sleep(0.2)
        raise AssertionError(f"route {src} -> {dst} is {self.route(src, dst)}, want {hops}")

    def wait_nodes(self, name, others, timeout=30):
        want = sorted(self.nodes[o].index for o in others)
        deadline = time.time() + timeout
        while time.time() < deadline:
            try:
                if self.known_nodes(name) == want:
                    return
            except (OSError, json.JSONDecodeError):
                pass
            time.sleep(0.2)
        raise AssertionError(f"{name}: knows {self.known_nodes(name)}, want {want}")


def main(test):
    unshared()
    test()
