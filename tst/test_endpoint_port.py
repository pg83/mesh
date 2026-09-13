"""Changing only a UDP port migrates the same SSH connection to a new vertex."""
import struct
import lib
import workload


def test():
    with lib.Lab(['a', 'b'], {1: ['a', 'b']}, statics=['a']) as lab:
        lab.wait_ping('a', 'b')
        stream = workload.SshServer(lab, 'b').stream('a')
        lab.stop_node('b')
        lab.configs['b'] = dict(endpoint=[dict(proto="udp", addr="0.0.0.0", port=7001)])
        lab.start_node('b')
        lab.wait(lambda: lab.selected_endpoint('a', 'b') == '10.1.0.2:7001', 'new destination port')
        sent = lab.intercept('a', 'b', 'copy', kind=3, count=-1, target_port=7001)
        stream.progress()
        stream.progress()
        assert sent['held']
        for _, packet, _ in sent['held']:
            assert struct.unpack_from('!H', packet, (packet[0] & 15)*4+2)[0] == 7001
        stream.finish()


lib.main(test)
