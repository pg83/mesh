"""19: A bounded stream of invalid UDP packets cannot starve SSH or QUIC."""
import json
import sys
import time
import lib
import workload


def test():
    with lib.Lab(['a', 'b', 'x'], {1: ['a', 'b', 'x']}) as lab:
        lab.wait_ping('a', 'b')
        lab.stop_node('x')
        lab.wait_links('a', ['b'])
        lab.wait_links('b', ['a'])
        server = workload.QuicServer(lab, 'b')
        client = server.client('a', seconds=20)
        ssh = workload.SshServer(lab, 'b').stream('a')
        client.start()
        code = '''
import json, socket, struct, sys, time
sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
end = time.monotonic() + 15
count = 0
while time.monotonic() < end:
    for _ in range(20):
        packet = bytes([3]) + struct.pack('<HQ', 3, time.monotonic_ns()) + bytes(100)
        sock.sendto(packet, (sys.argv[1], 7000))
        count += 1
    time.sleep(.02)
print(json.dumps({'packets': count}), flush=True)
'''
        flood = lab.spawn('x', [sys.executable, '-c', code, lab.nodes['b'].addresses[1]], 'invalid-flood')
        for _ in range(8):
            time.sleep(1)
            ssh.progress(timeout=5)
        client.progress()
        ssh.finish()
        assert ssh.max_gap < 5
        assert flood.wait(timeout=20) == 0
        assert json.loads((lab.dir / 'invalid-flood.log').read_text())['packets'] >= 10000
        assert lab.links('b') == {lab.nodes['a'].index}
        server.finish([client])


lib.main(test)
