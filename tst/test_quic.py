"""Four real QUIC clients stress one server for thirty seconds without reconnecting."""

import json
import os
import select
import subprocess

import lib
import workload


def test():
    clients = ['a', 'b', 'c', 'd']
    names = ['s', *clients]
    with lib.Lab(names, {1: names}) as lab:
        for name in clients:
            lab.wait_ping(name, 's')
        binary = os.environ['MESH_TEST_QUIC']
        host = workload.address(lab, 's') + ':9000'
        cert = lab.dir / 'quic.pem'
        server = lab.spawn('s', [binary, 'server', host, cert], 'quic-server')
        server_log = lab.dir / 'quic-server.log'
        def events():
            return [json.loads(line) for line in server_log.read_text().splitlines()
                    if line.startswith('{')]
        def ready():
            assert server.poll() is None, server_log.read_text()
            return any(event.get('event') == 'ready' for event in events())
        lab.wait(ready, 'QUIC server listening')
        processes = {}
        for name in clients:
            proc = lab.spawn(name, [binary, 'client', host, cert, '30s'], 'quic-' + name,
                             stdin=subprocess.PIPE, stdout=subprocess.PIPE)
            processes[name] = proc
            assert select.select([proc.stdout], [], [], 15)[0], f'{name}: QUIC connect timed out'
            report = json.loads(proc.stdout.readline())
            assert report['event'] == 'ready', report
        # All four connections are established before any client starts its clock.
        for proc in processes.values():
            proc.stdin.write(b'go\n')
            proc.stdin.flush()
        reports = {}
        for name, proc in processes.items():
            output, _ = proc.communicate(timeout=50)
            assert proc.returncode == 0, (name, output)
            report = json.loads(output)
            assert report['event'] == 'done', report
            assert report['seconds'] >= 30, report
            assert report['rounds'] >= 16, report
            assert report['bytes'] == report['rounds'] * (64 << 10), report
            reports[name] = report
            print(f"QUIC {name}: {report['bytes']} verified bytes in {report['seconds']:.2f}s, "
                  f"{report['bytes'] / report['seconds'] / (1 << 20):.2f} MiB/s each way", flush=True)
        lab.wait(lambda: sum(e.get('event') == 'done' for e in events()) == 4, 'four QUIC transfers completed')
        accepted = [e['peer'].split(':')[0] for e in events() if e.get('event') == 'connected']
        assert sorted(accepted) == sorted(workload.address(lab, n) for n in clients), accepted
        received = {e['peer'].split(':')[0]: e['bytes'] for e in events() if e.get('event') == 'done'}
        assert received == {workload.address(lab, n): r['bytes'] for n, r in reports.items()}, received
        assert server.poll() is None, server_log.read_text()
        lab.check()


lib.main(test)
