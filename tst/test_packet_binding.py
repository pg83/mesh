"""17: Captured ciphertext cannot be reflected or delivered to another registered peer."""
import lib
import work_load as workload


def test():
    lab = lib.Lab(['a', 'b', 'c'], {1: ['a', 'b', 'c']})
    lab.block('a', 'c')
    lab.block('b', 'c')
    with lab:
        lab.wait_ping('a', 'b')
        lab.wait_links('c', [])
        log = workload.udp_server(lab, 'b')
        udp = workload.UdpClient(lab, 'a', 'b')
        saved = lab.intercept('a', 'b', 'copy', kind=0, min_size=900)
        payload = b'bound-to-a-and-b'.ljust(900, b'.')
        udp.send(payload)
        assert udp.recv() == payload
        lab.replay(saved, src='b', dst='a')
        lab.replay(saved, src='b', dst='a', transform=lambda p: p[:1] + b'\x02\0' + p[3:])
        lab.replay(saved, dst='c')
        lab.replay(saved, src='b', dst='c', transform=lambda p: p[:1] + b'\x02\0' + p[3:])
        assert udp.recv(.3) is None
        assert lab.links('c') == set()
        assert lab.links('a') == {lab.nodes['b'].index}
        assert log.read_text().splitlines().count(payload.hex()) == 1
        lab.wait_ping('a', 'b')


lib.main(test)
