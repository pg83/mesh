"""Every packet carries its socket source; no preliminary message or reply is needed."""
import json
import socket
import struct
import sys
import time
import lib
import ws


def udp_payload(payload):
    udp = struct.pack('!HHHH', 9001, 9020, 8 + len(payload), 0) + payload
    header = bytearray(struct.pack('!BBHHHBBH4s4s', 0x45, 0, 20 + len(udp), 0, 0, 64, 17, 0,
                                   socket.inet_aton(lib.intip(1)), socket.inet_aton(lib.intip(2))))
    checksum = sum(struct.unpack('!10H', header))
    while checksum >> 16:
        checksum = (checksum & 65535) + (checksum >> 16)
    struct.pack_into('!H', header, 10, ~checksum & 65535)
    return header + udp


def test():
    with ws.Lab(names=['a', 'b', 'c'], statics=['b']) as lab:
        lab.wait_ping('a', 'b')
        lab.stop_node('a')
        lab.stop_node('c')
        server = dict(proto='ws', addr='10.1.0.2', port=7100, path='/mesh', endpoint=True)
        host_a, host_b, host_c = [lib.endpoint(lib.intip(i), 0) for i in [1, 2, 3]]
        log = lab.dir / 'packet-source-receiver.log'
        lab.spawn('b', [sys.executable, '-u', '-c',
                       "import socket; s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); s.bind(('10.77.0.2',9020)); print('ready',flush=True);\nwhile True: print(s.recv(65535).hex(),flush=True)"],
                  'packet-source-receiver')
        lab.wait(lambda: log.exists() and 'ready' in log.read_text(), 'payload receiver ready')
        previous = None
        for kind in ['graph', 'registry', 'data']:
            probe = ws.Probe(lab)
            source = probe.source
            assert source['proto'] == 'tcp' and source['addr'] == '10.1.0.1'
            assert source['port'] != 0 and not source['endpoint']
            assert previous is None or source != previous
            source_id = lib.endpoint_hash(source)
            attempts = lab.intercept('b', 'a', 'observe', target_port=source['port'], syn=True, count=-1)
            reverse = lab.intercept('b', 'a', 'drop', count=-1)
            marker = lib.socket_vertex('192.0.2.123', 49152)
            if kind == 'graph':
                ident = time.time_ns()
                command = dict(op='graph', body=lib.record(1, ident, [(marker, False, True), (source, False, True)]))
                applied = lambda: str(lib.endpoint_hash(marker)) in lab.status('b')['addresses']
            elif kind == 'registry':
                def string(value):
                    value = value.encode()
                    return struct.pack('<H', len(value)) + value
                record = struct.pack('<HQ', 50, 9) + lib.ipbytes(lib.intip(50)) + string(lab.nodes['a'].keys['pub']) + string('source-first') + b'\0\0'
                command = dict(op='inner', hex=(b'\4\1\0' + record).hex())
                applied = lambda: any(p['index'] == 50 and p['version'] == 9 for p in lab.status('b')['registry'])
            else:
                data = bytes([1, 1, 0, lab.nodes['b'].index])
                command = dict(op='inner', hex=(data + udp_payload(b'first-message-is-data')).hex())
                applied = lambda: b'first-message-is-data'.hex() in log.read_text()
            assert probe.send(**command)['sent']
            lab.wait(applied, kind + ' processed while all reverse traffic is blocked', timeout=3)
            state = lab.status('b')
            assert state['addresses'][str(source_id)] == source
            assert sum(source_id in (c['from'], c['to']) for c in state['channels']) == 2
            assert attempts['hits'] == 0, 'client address was used as a listener'
            lab.clear(reverse)
            lab.clear(attempts)
            if kind == 'graph':
                lab.wait(lambda: any(e['from'] == source and e['to'] == server for e in lab.status('b')['graph']), 'link resolved against the source owner record')
                probe.send(op='graph', body=lib.record(1, ident + 1, [(dict(marker, endpoint=True), True, False), (source, False, True)]))
                lab.wait(lambda: lab.status('b')['addresses'][str(lib.endpoint_hash(marker))]['endpoint'], 'endpoint flag updated for an existing vertex')
            probe.finish()
            lab.wait(lambda: not any(source_id in (c['from'], c['to']) for c in lab.status('b')['channels']), 'closed socket channels removed')
            lab.wait(lambda: not any(e['from'] == source for e in lab.status('b')['graph']), 'closed socket link withdrawn')
            previous = source


lib.main(test)
