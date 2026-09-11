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
        server = lab.spawn('b', ['iperf3', '-s', '-B', host, '-p', '5201'], 'iperf-server')
        workload.wait_port(lab, 'b', host, 5201, server)
        def run(*args):
            result = lab.run('a', ['iperf3', '-c', host, '-p', '5201', '-t', '3', '-J', *args], check=False)
            assert result.returncode == 0, (result.stdout, result.stderr)
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
        drop = lab.intercept('a', 'r', 'drop', count=-1, kind=3, every=20)
        delay = lab.intercept('r', 'a', 'delay', count=-1, kind=3, delay=.01)
        assert run('-P', '2')['sum_received']['bytes'] > 0
        end = run('-u', '-b', '1M', '-l', '1200')
        assert 0 < end['sum']['lost_percent'] < 20, end
        assert drop['hits'] and delay['hits']
        lab.clear(drop)
        lab.clear(delay)
        assert run()['sum_received']['bytes'] > 0


lib.main(test)
