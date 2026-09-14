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
import hashlib
import json
import ipaddress
import http.client
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
CONTROL = "127.0.0.1:8058"

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
    return dict(proto='udp', addr=address, port=port, endpoint=port != 0)


def socket_vertex(address, port, proto='udp'):
    return dict(proto=proto, addr=address, port=port, endpoint=False)


def tagged_vertex(address, port, target, owner):
    """The vertex of an outgoing UDP channel: the sending listener tagged with its target and owner."""
    return dict(proto='udp', addr=address, port=port, endpoint=False,
                target=target if isinstance(target, int) else endpoint_hash(target), owner=owner)


def vertex(value):
    return dict(value, endpoint=value.get('endpoint', value['port'] != 0 and value['proto'] != 'tcp'))


def endpoint_address(ep):
    return ep['addr']


def endpoint_hash(ep):
    if ep['addr'] in ('', '0.0.0.0', '::'):
        return 0
    value = '\0'.join([ep['proto'], ep['addr'].lower(), str(ep['port']), ep.get('path', '')])
    if ep.get('target'):
        value += '\0' + str(ep['target']) + '\0' + str(ep['owner'])
    return int.from_bytes(hashlib.sha256(value.encode()).digest()[:8], 'little')


def record(owner, version, vertices=(), links=(), observed=()):
    """A node's graph record: vertices as (vertex, ingress, egress), links as (source, target),
    observed as (source, seen address)."""
    def ident(value):
        return value if isinstance(value, int) else endpoint_hash(value)
    return dict(owner=owner, version=version,
                vertices=[dict(vertex(v), ingress=ingress, egress=egress) for v, ingress, egress in vertices],
                links=[{'from': ident(source), 'to': ident(target)} for source, target in links],
                observed=[{'from': ident(source), 'seen': seen} for source, seen in observed])


def segaddr(seg, index):
    return f"10.{seg}.0.{index}"


def ipbytes(address):
    return ipaddress.ip_address(address).packed


def prefix(address):
    return f'{address}/{64 if ":" in address else 24}'


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
    def __init__(self, nodes, segments, statics=None, ipv6=()):
        """nodes: names, index is position + 1. segments: {seg: [names]}.
        statics: names that publish their segment addresses; default all."""
        self.nodes = {n: Node(n, i + 1) for i, n in enumerate(nodes)}
        self.segments = segments
        self.statics = set(nodes if statics is None else statics)
        self.ipv6 = set(ipv6)
        self.dir = Path(tempfile.mkdtemp(prefix="mesh-lab-"))
        self.tuns = {}  # fd -> (seg, node)
        self.ports = {}  # (seg, addr bytes) -> fd
        self.running = True
        self.lock = threading.RLock()
        self.rules = []
        self.blocked = set()
        self.fragments = {}
        self.forwards = {}  # public UDP pair -> (node name, segment, local IP bytes, local port)
        self.counts = collections.Counter()
        self.delayed = []
        self.serial = 0
        self.switch_error = None
        self.thread = None
        self.processes = []
        self.configs = {}
        self.run_args = {}
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
        addr = f'2001:db8:{seg}::{node.index}' if seg in self.ipv6 else segaddr(seg, node.index)
        node.addresses[seg] = addr
        self.nsenter(node, "ip", "addr", "add", prefix(addr), "dev", name,
                     *(['nodad'] if seg in self.ipv6 else []), check=True)
        # A full periodic graph fanout can exceed the default 500-packet TUN
        # queue before the userspace switch is scheduled. Faults are injected
        # by the switch; leave room for a publication in the 18-node topology.
        self.nsenter(node, "ip", "link", "set", name, "txqueuelen", "16384", "up", check=True)
        self.tuns[fd] = (seg, node)
        self.ports[(seg, ipbytes(addr))] = fd

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
                  min_size=0, max_size=None, every=1, delay=0, rate=None, source_ip=None, target_ip=None, source_port=None, target_port=None, syn=False, proto=None):
        rule = dict(src=src, dst=dst, action=action, count=count, kind=kind, proto=proto,
                    seg=seg, min_size=min_size, max_size=max_size, every=every, delay=delay,
                    rate=rate, source_ip=source_ip, target_ip=target_ip,
                    source_port=source_port, target_port=target_port, syn=syn, next=0, seen=0, hits=0, held=[])
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

    def forward(self, name, seg, public_addr, public_port, bind_port):
        """The router translates both directions of a fixed UDP port mapping."""
        with self.lock:
            self.forwards[(socket.inet_aton(public_addr), public_port)] = (
                name, seg, socket.inet_aton(self.nodes[name].addresses[seg]), bind_port)

    def route_packet(self, source, seg, packet):
        if packet[0] >> 4 == 6:
            return self.ports.get((seg, packet[24:40])), packet
        out = self.ports.get((seg, packet[16:20]))
        head = (packet[0] & 15) * 4
        if packet[9] != 17 or len(packet) < head + 8:
            return out, packet
        source_port, target_port = struct.unpack_from('!HH', packet, head)
        target = self.forwards.get((packet[16:20], target_port))
        origin = next((public for public, local in self.forwards.items()
                       if local == (source, seg, packet[12:16], source_port)), None)
        if target is None and origin is None:
            return out, packet
        packet = bytearray(packet)
        if target is not None:
            _, target_seg, target_addr, target_port = target
            out = self.ports[(target_seg, target_addr)]
            packet[16:20] = target_addr
            struct.pack_into('!H', packet, head + 2, target_port)
        if origin is not None:
            packet[12:16] = origin[0]
            struct.pack_into('!H', packet, head, origin[1])
        packet[head + 6:head + 8] = b'\0\0'
        packet[10:12] = b'\0\0'
        checksum = sum(struct.unpack('!' + 'H' * (head // 2), packet[:head]))
        while checksum >> 16:
            checksum = (checksum & 0xffff) + (checksum >> 16)
        struct.pack_into('!H', packet, 10, ~checksum & 0xffff)
        return out, bytes(packet)

    def reassemble(self, fd, packet):
        if packet[0] >> 4 != 4:
            return packet
        flags, = struct.unpack_from('!H', packet, 6)
        if not flags & 0x3fff:
            return packet
        now = time.monotonic()
        self.fragments = {k: v for k, v in self.fragments.items() if now-v['at'] < 10}
        key = (fd, packet[4:6], packet[9], packet[12:20])
        state = self.fragments.setdefault(key, dict(at=now, parts={}, header=None, size=None))
        head = (packet[0] & 15)*4
        offset = (flags & 0x1fff)*8
        state['parts'][offset] = packet[head:]
        if offset == 0:
            state['header'] = packet[:head]
        if not flags & 0x2000:
            state['size'] = offset+len(packet)-head
        if state['header'] is None or state['size'] is None:
            return None
        body = bytearray()
        for offset, part in sorted(state['parts'].items()):
            if offset != len(body):
                return None
            body.extend(part)
        if len(body) != state['size']:
            return None
        del self.fragments[key]
        packet = bytearray(state['header'])+body
        struct.pack_into('!H', packet, 6, 0)
        struct.pack_into('!H', packet, 2, len(packet))
        packet[10:12] = b'\0\0'
        head = (packet[0] & 15)*4
        checksum = sum(struct.unpack('!'+'H'*(head//2), packet[:head]))
        while checksum >> 16:
            checksum = (checksum & 0xffff)+(checksum >> 16)
        struct.pack_into('!H', packet, 10, ~checksum & 0xffff)
        return bytes(packet)

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
                        version = packet[0] >> 4
                        if len(packet) < (40 if version == 6 else 20) or version not in (4, 6):
                            continue
                        packet = self.reassemble(fd, packet)
                        if packet is None:
                            continue
                        seg, src = self.tuns[fd]
                        out, packet = self.route_packet(src.name, seg, packet)
                        if out is None or out == fd:
                            continue
                        dst = self.tuns[out][1]
                        key = (src.name, dst.name, seg)
                        if key in self.blocked or (src.name, dst.name, None) in self.blocked:
                            self.counts[(*key, 'dropped')] += 1
                            continue
                        head = 40 if version == 6 else (packet[0] & 15) * 4
                        proto = packet[6] if version == 6 else packet[9]
                        source_ip = packet[8:24] if version == 6 else packet[12:16]
                        target_ip = packet[24:40] if version == 6 else packet[16:20]
                        payload = packet[head + 8:] if proto == 17 else b''
                        for rule in self.rules:
                            if (rule['src'] != src.name or rule['dst'] != dst.name
                                    or (rule['source_ip'] is not None and source_ip != ipbytes(rule['source_ip']))
                                    or (rule['target_ip'] is not None and target_ip != ipbytes(rule['target_ip']))
                                    or (rule['proto'] is not None and proto != rule['proto'])
                                    or (rule['syn'] and (proto != 6 or len(packet) < head+20 or packet[head+13] & 0x12 != 0x02))
                                    or (rule['source_port'] is not None and (proto not in (6, 17) or struct.unpack_from('!H', packet, head)[0] != rule['source_port']))
                                    or (rule['target_port'] is not None and (proto not in (6, 17) or struct.unpack_from('!H', packet, head + 2)[0] != rule['target_port']))
                                    or rule['count'] == 0
                                    or rule['seg'] not in (None, seg)
                                    or len(payload) < rule['min_size']
                                    or (rule['max_size'] is not None and len(payload) > rule['max_size'])
                                    or (rule['kind'] is not None and (not payload or payload[0] & 3 != rule['kind']))):
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

    def add_address(self, name, seg, address):
        node = self.nodes[name]
        self.nsenter(node, 'ip', 'addr', 'add', prefix(address), 'dev', f's{seg}',
                     *(['nodad'] if ':' in address else []), check=True)
        with self.lock:
            self.ports[(seg, ipbytes(address))] = self.ports[(seg, ipbytes(node.addresses[seg]))]

    def set_address(self, name, seg, address):
        node = self.nodes[name]
        with self.lock:
            old = node.addresses[seg]
            fd = self.ports.pop((seg, ipbytes(old)))
            self.nsenter(node, 'ip', 'addr', 'del', prefix(old), 'dev', f's{seg}', check=True)
            self.nsenter(node, 'ip', 'addr', 'add', prefix(address), 'dev', f's{seg}',
                         *(['nodad'] if ':' in address else []), check=True)
            self.ports[(seg, ipbytes(address))] = fd
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
                static = [dict(proto="udp", addr=node.addresses[seg], port=PORT) for seg in node.segments]
            reg.append({
                "name": node.name,
                "index": node.index,
                "pub": node.keys["pub"],
                "intip": intip(node.index),
                "endpoint": static,
            })
        return reg

    def write_config(self, node):
        cfg = {
            "index": node.index,
            "key": node.keys["key"],
            "endpoint": [dict(proto="udp", addr=addr, port=PORT)
                         for addr in (['0.0.0.0', '::'] if self.ipv6 else ['0.0.0.0'])],
            "subnet": SUBNET,
            "control": CONTROL,
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
        node.proc = self.spawn(name, [MESH, 'run', '-c', self.write_config(node), *self.run_args.get(name, [])], name, env=env)

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

    def http(self, name, path, port=8058):
        self.check()
        node = self.nodes[name]
        with open(self.netns(node.pid)) as target, open('/proc/thread-self/ns/net') as current:
            try:
                os.setns(target.fileno(), os.CLONE_NEWNET)
                conn = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            finally:
                os.setns(current.fileno(), os.CLONE_NEWNET)
        with conn:
            conn.settimeout(10)
            conn.connect(('127.0.0.1', port))
            conn.sendall(f'GET {path} HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n'.encode())
            response = http.client.HTTPResponse(conn)
            response.begin()
            return response.status, dict(response.getheaders()), response.read()

    def status(self, name):
        """Asks the node itself, from inside its namespace."""
        try:
            code, _, data = self.http(name, '/status')
            if code != 200:
                raise OSError(f'control HTTP {code}')
        except OSError as error:
            raise OSError(f'{name}: status failed: {error}') from error
        status = json.loads(data)
        descriptors = status['addresses']
        def decode(edge):
            return dict(edge, **{k: descriptors[str(edge[k])] for k in ('from', 'to')})
        status['vertices'] = [descriptors[str(i)] for i in status['vertices']]
        for key in ('links', 'graph'):
            status[key] = [decode(edge) for edge in status[key]]
        status['routes'] = {key: [decode(edge) for edge in path] for key, path in status['routes'].items()}
        return status

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

    def full_route(self, src, dst):
        return self.status(src)['routes'].get(intip(self.nodes[dst].index) + ':0')

    def endpoint_route(self, src, dst):
        path = self.full_route(src, dst)
        if path is None:
            return None
        assert path[0]['from'] == endpoint(intip(self.nodes[src].index), 0), path
        assert path[-1]['to'] == endpoint(intip(self.nodes[dst].index), 0), path
        def host(ep):
            return ep['proto'] == 'udp' and ep['port'] == 0
        return [edge for edge in path if not host(edge['from']) and not host(edge['to'])]

    def channel_source(self, name, address, target=None, proto='udp'):
        state = self.status(name)
        for channel in state['channels']:
            if not channel['outgoing']:
                continue
            src, dst = [state['addresses'][str(channel[key])] for key in ['from', 'to']]
            if src['addr'] == address and src['proto'] == proto and (target is None or dst['addr'] == target):
                return src
        return None

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
