"""Native macOS utun: real ICMP over encrypted UDP to an independent echo peer."""
import json
import os
from contextlib import contextmanager
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.request

binary = Path('.build/bin/mesh').resolve()
probe = Path('.build/bin/mesh-probe').resolve()

def run(*args):
    return subprocess.check_output([str(a) for a in args], text=True)

@contextmanager
def software_checksums():
    # BPF sees partial checksums when loopback checksum offload is enabled.
    name = 'net.link.generic.system.hwcksum_tx'
    previous = run('/usr/sbin/sysctl', '-n', name).strip()
    run('/usr/sbin/sysctl', '-w', name+'=0')
    try:
        yield
    finally:
        run('/usr/sbin/sysctl', '-w', name+'='+previous)

with software_checksums(), tempfile.TemporaryDirectory(prefix='mesh-darwin-') as directory:
    root = Path(directory)
    a, b = [json.loads(run(binary, 'keygen')) for _ in range(2)]
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
        sock.connect(('192.0.2.1', 9))
        host = sock.getsockname()[0]
    registry = [dict(name='mac', index=1, pub=a['pub'], intip='10.77.0.1', endpoint=[]),
                dict(name='echo', index=2, pub=b['pub'], intip='10.77.0.2',
                     endpoint=[dict(proto='udp', addr=host, port=17002)])]
    peer_cfg = dict(index=2, key=b['key'], registry=registry,
                    endpoint=[dict(proto='udp', addr=host, port=17002)])
    (root/'peer.json').write_text(json.dumps(peer_cfg))
    (root/'node.json').write_text(json.dumps(dict(index=1, subnet='10.77.0.0/24', mtu=1380,
        control='127.0.0.1:18058', registry=registry, endpoint=[dict(proto='udp', addr=host, port=17001)])))
    (root/'key').write_text(a['key'])
    capture_log = open(root/'udp-checksums.log', 'w+')
    capture_err = open(root/'tcpdump.log', 'w+')
    capture = subprocess.Popen(['/usr/sbin/tcpdump', '-i', 'lo0', '-nn', '-l', '-vv',
        'udp and dst port 17002'], stdout=capture_log, stderr=capture_err)
    peer_log = open(root/'peer.log', 'w+')
    peer_process = subprocess.Popen([probe, 'echo', root/'peer.json', f'{host}:17001', '1'], stdout=peer_log, stderr=peer_log)
    try:
        deadline = time.monotonic()+10
        while time.monotonic()<deadline:
            capture_err.seek(0)
            if 'listening on' in capture_err.read():
                break
            assert capture.poll() is None, 'tcpdump exited'
            time.sleep(.1)
        else:
            raise AssertionError('tcpdump did not start')
        # A second run verifies that utun and its route disappear on exit.
        for attempt in range(2):
            cfg=json.loads((root/'node.json').read_text())
            cfg['endpoint']=[dict(proto=proto,addr=host,port=17001) for proto in (['udp'] if attempt==0 else ['udp','ws'])]
            (root/'node.json').write_text(json.dumps(cfg))
            node_log = open(root/f'node-{attempt}.log', 'w+')
            node = subprocess.Popen([binary, 'run', '-c', root/'node.json', '-key-file', root/'key'], stdout=node_log, stderr=node_log)
            try:
                deadline = time.monotonic()+20
                while time.monotonic()<deadline:
                    assert node.poll() is None, 'node exited'
                    try:
                        with urllib.request.urlopen('http://127.0.0.1:18058/status', timeout=1) as r:
                            status=json.load(r)
                        if status['routes'].get('10.77.0.2:0'):
                            break
                    except OSError:
                        pass
                    time.sleep(.1)
                else:
                    raise AssertionError('no route to echo peer')
                route = run('/sbin/route', '-n', 'get', '10.77.0.2')
                assert 'utun' in route, route
                for size in [56, 1200]:
                    result=run('/sbin/ping', '-n', '-c', '4', '-s', str(size), '-W', '1000', '10.77.0.2')
                    assert ' 0.0% packet loss' in result, result
                print(f'Darwin TUN round trip {attempt+1}: 8 ICMP packets, both sizes passed')
            finally:
                node.terminate()
                code=node.wait(timeout=5)
                node_log.seek(0)
                print(node_log.read())
                node_log.close()
                assert code==0, code
    finally:
        peer_process.terminate()
        peer_process.wait(timeout=5)
        peer_log.seek(0)
        print(peer_log.read())
        peer_log.close()
        capture.terminate()
        capture.wait(timeout=5)
        capture_log.seek(0)
        packets = capture_log.read()
        capture_log.close()
        capture_err.close()
    assert '[bad udp cksum' not in packets, 'bad UDP checksum on Darwin:\n'+packets
    assert '[udp sum ok]' in packets, 'no verified UDP packets captured:\n'+packets
    print('Darwin outgoing UDP checksums verified on the captured packets')


@contextmanager
def ipv6_addresses(iface, addresses):
    added = []
    try:
        for address in addresses:
            run('/sbin/ifconfig', iface, 'inet6', address, 'prefixlen', '64', 'alias')
            added.append(address)
        yield added
    finally:
        for address in added:
            run('/sbin/ifconfig', iface, 'inet6', address, '-alias')


def wait_for(predicate, description, timeout=15):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(.05)
    raise AssertionError(description)


def status():
    try:
        with urllib.request.urlopen('http://127.0.0.1:18058/status', timeout=1) as response:
            return json.load(response)
    except OSError:
        return {}


def attached(address):
    state = status()
    endpoints = state.get('addresses', {})
    return any(endpoints[str(e['from'])]['addr'] == address
               and endpoints[str(e['from'])]['proto'] == 'udp'
               and endpoints[str(e['to'])]['addr'] == '10.77.0.1' for e in state.get('graph', []))


def can_send(address):
    state = status()
    return any(c['outgoing'] and state['addresses'][str(c['from'])]['addr'] == address
               for c in state.get('channels', []))


# Alias the runner's physical interface: loopback interfaces are intentionally
# excluded from mesh discovery. No external IPv6 connectivity is required.
iface = None
for line in run('/sbin/ifconfig').splitlines():
    if line and not line[0].isspace():
        current_iface = line.split(':', 1)[0]
    if line.strip().startswith('inet '+host+' '):
        iface = current_iface
assert iface, 'could not find physical interface'
old, peer_addr, new = 'fd77:abcd::1', 'fd77:abcd::2', 'fd77:abcd::3'
with software_checksums(), ipv6_addresses(iface, [old, peer_addr]) as aliases, tempfile.TemporaryDirectory(prefix='mesh-darwin6-') as directory:
    root = Path(directory)
    a, b = [json.loads(run(binary, 'keygen')) for _ in range(2)]
    registry = [dict(name='mac', index=1, pub=a['pub'], intip='10.77.0.1', endpoint=[]),
                dict(name='echo', index=2, pub=b['pub'], intip='10.77.0.2',
                     endpoint=[dict(proto='udp', addr=peer_addr, port=17002)])]
    (root/'node.json').write_text(json.dumps(dict(index=1, key=a['key'], subnet='10.77.0.0/24', mtu=1380,
        control='127.0.0.1:18058', registry=registry, endpoint=[dict(proto='udp', addr='::', port=17001)])))
    (root/'peer.json').write_text(json.dumps(dict(index=2, key=b['key'], registry=registry,
        endpoint=[dict(proto='udp', addr=peer_addr, port=17002)])))
    wait_for(lambda: all('tentative' not in line for line in run('/sbin/ifconfig', iface).splitlines()
                         if old in line or peer_addr in line), 'IPv6 DAD did not finish')
    processes = []
    with open(root/'node.log', 'w+') as node_log, open(root/'peer.log', 'w+') as peer_log, \
         open(root/'packets.log', 'w+') as packets_log, open(root/'capture.log', 'w+') as capture_log:
        try:
            capture = subprocess.Popen(['/usr/sbin/tcpdump', '-i', 'lo0', '-nn', '-l', '-vv',
                'ip6 and udp and dst port 17002'], stdout=packets_log, stderr=capture_log)
            processes.append(capture)
            def capture_ready():
                capture_log.seek(0)
                return 'listening on' in capture_log.read()
            wait_for(capture_ready, 'IPv6 tcpdump did not start')
            peer_process = subprocess.Popen([probe, 'echo', root/'peer.json', '['+old+']:17001', '1'], stdout=peer_log, stderr=peer_log)
            processes.append(peer_process)
            node = subprocess.Popen([binary, 'run', '-c', root/'node.json'], stdout=node_log, stderr=node_log)
            processes.append(node)
            wait_for(lambda: status().get('routes', {}).get('10.77.0.2:0'), 'no route over IPv6')
            for size in [56, 1200]:
                result = run('/sbin/ping', '-n', '-c', '4', '-s', str(size), '-W', '1000', '10.77.0.2')
                assert ' 0.0% packet loss' in result, result
            assert attached(old), status()
            run('/sbin/ifconfig', iface, 'inet6', old, '-alias')
            aliases.remove(old)
            wait_for(lambda: not attached(old), 'Darwin address removal notification', timeout=3)
            run('/sbin/ifconfig', iface, 'inet6', new, 'prefixlen', '64', 'alias')
            aliases.append(new)
            # Listener attachment checks the OS event, independently of IPv6
            # duplicate-address detection delaying a new outgoing channel.
            wait_for(lambda: attached(new), 'Darwin address addition notification', timeout=3)
            wait_for(lambda: can_send(new), 'new IPv6 source channel after address validation')
            wait_for(lambda: status().get('routes', {}).get('10.77.0.2:0'), 'IPv6 route after roaming')
            result = run('/sbin/ping', '-n', '-c', '4', '-W', '1000', '10.77.0.2')
            assert ' 0.0% packet loss' in result, result
            assert node.poll() is None
        finally:
            for process in reversed(processes):
                process.terminate()
                process.wait(timeout=5)
            for log in [node_log, peer_log, capture_log]:
                log.seek(0)
                print(log.read())
        packets_log.seek(0)
        packets = packets_log.read()
        assert '[bad udp cksum' not in packets, packets
        assert '[udp sum ok]' in packets, packets
        print('Darwin IPv6: encrypted TUN round trips, checksum and address events passed')
