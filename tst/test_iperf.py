"""Real iperf3 TCP streams and UDP traffic over a delayed, lossy mesh link."""

import json

import lib
import workload


def test():
    workload.require('iperf3')
    with lib.Lab(['a', 'r', 'b'], {1: ['a', 'r'], 2: ['r', 'b']}) as lab:
        lab.wait_route('a', 'b', ['r', 'b'])
        lab.wait_route('b', 'a', ['r', 'a'])
        lab.wait_ping('a', 'b')
        host = workload.address(lab, 'b')
        sequence = 0
        def run(*args):
            nonlocal sequence
            sequence += 1
            label = f'iperf-server-{sequence}'
            server = lab.spawn('b', ['iperf3', '-s', '-1', '--forceflush', '-B', host, '-p', '5201'], label)
            log = lab.dir / f'{label}.log'
            def ready():
                assert server.poll() is None, log.read_text()
                return 'Server listening on 5201' in log.read_text()
            # A TCP readiness connection would consume this one-off server.
            lab.wait(ready, 'iperf server listening')
            result = lab.run('a', ['iperf3', '-c', host, '-p', '5201', '-t', '3', '-J', *args], check=False)
            assert result.returncode == 0, (result.stdout, result.stderr)
            assert server.wait(timeout=15) == 0, log.read_text()
            report = json.loads(result.stdout)
            assert 'error' not in report, report
            print(json.dumps(report['end']), flush=True)
            return report['end']
        for args in ([], ['-R'], ['-P', '4']):
            assert run(*args)['sum_received']['bytes'] > 0
        for size in ('64', '1200'):
            end = run('-u', '-b', '1M', '-l', size)
            assert end['sum']['packets'] > 100
            assert end['sum']['lost_percent'] < 10
        # iperf 3.16 sends its four-byte UDP setup request only once. Exercise
        # data loss without randomly losing setup before measurement starts.
        drop = lab.intercept('a', 'r', 'drop', count=-1, kind=0, every=20, min_size=1000)
        delay = lab.intercept('r', 'a', 'delay', count=-1, kind=0, delay=.01)
        assert run('-P', '2')['sum_received']['bytes'] > 0
        end = run('-u', '-b', '1M', '-l', '1200')
        assert 0 < end['sum']['lost_percent'] < 20, end
        assert drop['hits'] and delay['hits']
        lab.clear(drop)
        lab.clear(delay)
        assert run()['sum_received']['bytes'] > 0


lib.main(test)
