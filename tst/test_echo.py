"""Validate the independent peer used by native Darwin's TUN round-trip test."""
import json
import os
import lib


def test():
    with lib.Lab(['a', 'b'], {1: ['a', 'b']}) as lab:
        lab.wait_ping('a', 'b')
        lab.stop_node('b')
        cfg = json.loads((lab.dir/'b.json').read_text())
        cfg['endpoint'] = [lib.endpoint('10.1.0.2')]
        (lab.dir/'echo.json').write_text(json.dumps(cfg))
        proc = lab.spawn('b', [os.environ['MESH_TEST_PROBE'], 'echo', lab.dir/'echo.json', '10.1.0.1:7000', '1'], 'echo')
        lab.wait_ping('a', 'b')
        for size in [56, 1200]:
            result = lab.run('a', ['ping', '-n', '-c', '4', '-W', '2', '-s', str(size), '10.77.0.2'])
            assert '0% packet loss' in result.stdout, result.stdout
        proc.terminate()
        proc.wait(timeout=5)


lib.main(test)
