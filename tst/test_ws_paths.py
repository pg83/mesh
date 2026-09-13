"""Paths distinguish endpoints on one TCP listener; restarting on another path preserves IP traffic."""
import lib
import ws
import workload


class Paths(ws.Lab):
    second_only = False

    def registry(self):
        registry = super().registry()
        other = dict(registry[1]['endpoint'][0], path='/other?channel=2')
        registry[1]['endpoint'] = [other] if self.second_only else registry[1]['endpoint'] + [other]
        return registry


def test():
    with Paths() as lab:
        lab.wait_ping('a', 'b')
        lab.wait(lambda: len(lab.connections('a')) == len(lab.connections('b')) == 3,
                 'two paths remain distinct connections')
        workload.udp_server(lab, 'b')
        client = workload.UdpClient(lab, 'a', 'b')
        client.send(b'before-restart')
        assert client.recv() == b'before-restart'
        old = lab.connections('a')
        lab.stop_node('b')
        lab.second_only = True
        lab.configs['b'] = dict(endpoint=[])
        lab.start_node('b')
        lab.wait_ping('a', 'b')
        lab.wait(lambda: len(lab.connections('a')) == len(lab.connections('b')) == 2,
                 'old reader cannot delete replacement connection')
        assert lab.endpoint_route('a', 'b')[0]['to']['path'] == '/other?channel=2'
        assert lab.connections('a') != old
        client.send(b'after-restart')
        assert client.recv() == b'after-restart'


lib.main(test)
