"""Two nodes behind NAT learn their mappings from a public peer and connect directly."""
import json
import lib
import nat
import time
import work_load as workload


def observed(lab, observer, name):
    """Addresses the observer reports for the node's sockets."""
    node = lab.status(observer)
    index = lab.nodes[name].index
    out = set()
    for record in node['records']:
        if record['owner'] != lab.nodes[observer].index:
            continue
        for entry in record['observed']:
            source = node['addresses'][str(entry['from'])]
            if source['addr'] == lab.nodes[name].addresses[lab.nodes[name].segments[0]]:
                out.add((entry['seen']['addr'], entry['seen']['port'], source['port']))
    return out


def path(lab, src, dst):
    """Node names a packet from src visits on its way to dst, by the host vertices of the route."""
    try:
        route = lab.full_route(src, dst)
    except (OSError, json.JSONDecodeError):
        return None
    if route is None:
        return None
    names = {lib.intip(node.index): node.name for node in lab.nodes.values()}
    return [names[edge['to']['addr']] for edge in route if edge['to']['port'] == 0]


def wait_path(lab, src, dst, hops):
    lab.wait(lambda: path(lab, src, dst) == hops, f'route {src} -> {dst} is {hops}')


def test():
    with nat.PunchLab() as lab:
        wait_path(lab, 'a', 'r', ['r'])
        wait_path(lab, 'b', 'r', ['r'])
        # The public node sees both private sockets behind their NAT mappings.
        lab.wait(lambda: observed(lab, 'r', 'a') and observed(lab, 'r', 'b'), 'mappings observed by the public node')
        for name in ['a', 'b']:
            for addr, port, local in observed(lab, 'r', name):
                assert addr == lab.public(name) and port == local, (name, addr, port, local)
        # Both NATed nodes dial the observed mapping of the other; the first
        # packets open the mappings, the next ones pass, and the direct link
        # replaces the relay path.
        wait_path(lab, 'a', 'b', ['b'])
        wait_path(lab, 'b', 'a', ['a'])
        assert lab.filtered > 0, 'the NAT never filtered an unsolicited packet'
        for src, dst in [('a', 'b'), ('b', 'a')]:
            state = lab.status(src)
            direct = [c for c in state['channels'] if c['outgoing']
                      and state['addresses'][str(c['to'])]['addr'] == lab.nodes[dst].addresses[lab.nodes[dst].segments[0]]]
            assert len(direct) == 1, direct
            target = state['addresses'][str(direct[0]['to'])]
            assert direct[0]['wire'] == dict(addr=lab.public(dst), port=target['port']), direct[0]
        for name in ['a', 'b']:
            workload.udp_server(lab, name)
        clients = [workload.UdpClient(lab, 'a', 'b'), workload.UdpClient(lab, 'b', 'a')]
        for i, client in enumerate(clients):
            payload = bytes([i + 1]) * 1100
            client.send(payload)
            reply = client.recv()
            if reply != payload:
                for name in ['a', 'b']:
                    state = lab.status(name)
                    print(name, 'channels', state['channels'], 'links', state['links'], 'routes', state['routes'].keys(), 'filtered', lab.filtered, 'counts', dict(lab.counts))
                assert reply == payload, 'echo over the punched link'
        # The public node only introduced them; the direct link outlives it.
        lab.stop_node('r')
        time.sleep(6)
        assert path(lab, 'a', 'b') == ['b'] and path(lab, 'b', 'a') == ['a']
        for client in clients:
            client.send(b'without-the-introducer')
            assert client.recv() == b'without-the-introducer'


lib.main(test)
