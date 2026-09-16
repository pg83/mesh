"""curl download migrates to a relay; clients from different IPs fetch concurrently."""

import concurrent.futures
import sys

import lib
import work_load as workload


def test():
    workload.require('curl')
    segments = {1: ['a', 'b'], 2: ['a', 'r'], 3: ['r', 'b'], 4: ['c', 'b']}
    with lib.Lab(['a', 'b', 'r', 'c'], segments) as lab:
        lab.wait_route('a', 'b', ['b'])
        lab.wait_route('r', 'b', ['b'])
        lab.wait_route('c', 'b', ['b'])
        source, received = lab.dir / 'big.bin', lab.dir / 'download.bin'
        digest = workload.random_file(source, 8 << 20)
        host = workload.address(lab, 'b')
        server = lab.spawn('b', [sys.executable, '-m', 'http.server', '8080', '--bind', host,
                                  '--directory', lab.dir], 'http-server')
        workload.wait_port(lab, 'b', host, 8080, server)
        url = f'http://{host}:8080/big.bin'
        curl = ['curl', '--noproxy', '*', '-fSs', '--max-time', '120']
        proc = lab.spawn('a', [*curl, '--limit-rate', '256k', '-o', received, url], 'curl-transfer')
        lab.wait(lambda: received.exists() and received.stat().st_size >= 65536, 'curl started')
        assert proc.poll() is None
        lab.block('a', 'b')
        lab.wait_route('a', 'b', ['r', 'b'])
        assert proc.wait(timeout=120) == 0
        assert workload.sha(received) == digest
        lab.unblock('a', 'b')
        lab.wait_route('a', 'b', ['b'])
        def fetch(job):
            node, index = job
            target = lab.dir / f'{node}-{index}.bin'
            lab.run(node, [*curl, '-o', target, url])
            assert workload.sha(target) == digest
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
            list(pool.map(fetch, [(node, index) for node in ('a', 'c') for index in range(4)]))
        log = (lab.dir / 'http-server.log').read_text()
        for name in ('a', 'c'):
            assert workload.address(lab, name) in log


lib.main(test)
