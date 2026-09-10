"""E2E lab: mesh nodes in separate network namespaces, wired by a userspace
switch.

The kernel here has no veth, so every node gets one TUN per segment as its
"physical" interface and this process copies IP packets between TUNs of the
same segment. A segment is a LAN: nodes on it see each other directly,
nothing else. Requires unprivileged user namespaces; a test re-execs itself
under `unshare -rUn` and spawns one `unshare -n` holder per node.
"""

import fcntl
import json
import os
import select
import socket
import struct
import subprocess
import sys
import tempfile
import threading
import time
from pathlib import Path

MESH = Path(os.environ["MESH_TEST_BINARY"])
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
    env = dict(os.environ, MESH_TEST_UNSHARED="1")
    os.execvpe("unshare", ["unshare", "-rUn", sys.executable, *sys.argv], env)


def intip(index):
    return f"10.77.0.{index}"


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
        for seg, names in segments.items():
            for name in names:
                self.nodes[name].segments.append(seg)

    # --- namespaces and wires ---

    def netns(self, pid):
        return f"/proc/{pid}/ns/net"

    def nsenter(self, node, *cmd, **kw):
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
        while os.stat(self.netns(self.holder_pid(node))).st_ino == mine:
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
        self.nsenter(node, "ip", "addr", "add", f"{addr}/24", "dev", name, check=True)
        self.nsenter(node, "ip", "link", "set", name, "up", check=True)
        self.tuns[fd] = (seg, node)
        self.ports[(seg, socket.inet_aton(addr))] = fd

    def switch(self):
        fds = list(self.tuns)
        while self.running:
            ready, _, _ = select.select(fds, [], [], 0.2)
            for fd in ready:
                pkt = os.read(fd, 65536)
                if len(pkt) < 20 or pkt[0] >> 4 != 4:
                    continue
                seg, _ = self.tuns[fd]
                out = self.ports.get((seg, pkt[16:20]))
                if out is not None and out != fd:
                    os.write(out, pkt)

    # --- nodes ---

    def keygen(self, node):
        out = subprocess.run([MESH, "keygen"], stdout=subprocess.PIPE, check=True, text=True)
        node.keys = json.loads(out.stdout)

    def registry(self):
        reg = []
        for node in self.nodes.values():
            static = []
            if node.name in self.statics:
                static = [f"{segaddr(seg, node.index)}:{PORT}" for seg in node.segments]
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
        path = self.dir / f"{node.name}.json"
        path.write_text(json.dumps(cfg, indent=2))
        return path

    def start_node(self, name):
        node = self.nodes[name]
        log = open(self.dir / f"{name}.log", "ab")
        node.proc = subprocess.Popen(
            ["timeout", LIFETIME, "nsenter", "-t", str(node.pid), "-n", MESH, "run", "-c", self.write_config(node)],
            stdin=subprocess.DEVNULL, stdout=log, stderr=log,
        )

    def stop_node(self, name):
        node = self.nodes[name]
        node.proc.terminate()
        node.proc.wait()
        node.proc = None

    def start(self):
        for node in self.nodes.values():
            self.keygen(node)
            self.start_holder(node)
            for seg in node.segments:
                self.add_wire(node, seg)
        threading.Thread(target=self.switch, daemon=True).start()
        for name in self.nodes:
            self.start_node(name)

    def stop(self):
        self.running = False
        for node in self.nodes.values():
            if node.proc:
                node.proc.terminate()
            if node.holder:
                node.holder.terminate()

    def __enter__(self):
        self.start()
        return self

    def __exit__(self, kind, value, tb):
        if kind is not None:
            self.dump_logs()
        self.stop()

    def dump_logs(self):
        for name in self.nodes:
            path = self.dir / f"{name}.log"
            sys.stderr.write(f"--- {name} ---\n{path.read_text()}")

    # --- observations ---

    def status(self, name):
        """Asks the node itself, from inside its namespace."""
        node = self.nodes[name]
        r = self.nsenter(node, MESH, "status", "-s", STATUS,
                         stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        if r.returncode != 0:
            raise OSError(f"{name}: status failed")
        return json.loads(r.stdout)

    def links(self, name):
        try:
            return {link["peer"] for link in self.status(name)["links"]}
        except (OSError, json.JSONDecodeError):
            return set()

    def wait_links(self, name, peers, timeout=15):
        want = {self.nodes[p].index for p in peers}
        deadline = time.time() + timeout
        while time.time() < deadline:
            if self.links(name) == want:
                return
            time.sleep(0.2)
        raise AssertionError(f"{name}: links {self.links(name)} != {want} after {timeout}s")

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
        by_index = {node.index: name for name, node in self.nodes.items()}
        path = self.status(src)["routes"].get(str(self.nodes[dst].index))
        return [by_index[hop] for hop in path] if path else None

    def wait_route(self, src, dst, hops, timeout=30):
        deadline = time.time() + timeout
        while time.time() < deadline:
            if self.route(src, dst) == hops:
                return
            time.sleep(0.2)
        raise AssertionError(f"route {src} -> {dst} is {self.route(src, dst)}, want {hops}")

    def wait_nodes(self, name, others, timeout=30):
        want = sorted(self.nodes[o].index for o in others)
        deadline = time.time() + timeout
        while time.time() < deadline:
            try:
                if self.status(name)["nodes"] == want:
                    return
            except (OSError, json.JSONDecodeError):
                pass
            time.sleep(0.2)
        raise AssertionError(f"{name}: knows {self.status(name)['nodes']}, want {want}")


def main(test):
    unshared()
    test()
