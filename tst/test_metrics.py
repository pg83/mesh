"""The control /metrics endpoint exposes node counters and a consistent status snapshot."""
import time
import lib
import workload


def metrics(lab, name):
    code, headers, body = lab.http(name, '/metrics')
    assert code == 200, code
    assert headers['Content-Type'].startswith('text/plain'), headers
    values = {}
    for line in body.decode().splitlines():
        if line.startswith('#'):
            continue
        key, value = line.rsplit(' ', 1)
        values[key] = float(value)
    return values


def test():
    with lib.Lab(['a', 'r', 'b'], {1: ['a', 'r'], 2: ['r', 'b']}) as lab:
        lab.wait_route('a', 'b', ['r', 'b'])
        lab.wait_ping('a', 'b')
        log = workload.udp_server(lab, 'b')
        udp = workload.UdpClient(lab, 'a', 'b')
        for index in range(5):
            payload = str(index).encode().ljust(400, b'.')
            udp.send(payload)
            assert udp.recv() == payload
        m = metrics(lab, 'a')
        status = lab.status('a')
        assert m['mesh_up'] == 1
        assert m['mesh_graph_edges'] == len(status['graph'])
        assert m['mesh_graph_vertices'] == len(status['vertices'])
        assert m['mesh_addresses'] == len(status['addresses'])
        assert m['mesh_records'] == len(status['records']) == 3
        assert m['mesh_links'] == len(status['links']) == 1
        assert m['mesh_routes'] == len(status['routes'])
        assert m['mesh_channels{transport="udp",direction="outgoing"}'] >= 1
        assert m['mesh_channels{transport="udp",direction="incoming"}'] == 1
        assert m['mesh_peer_reachable{peer="r"}'] == 1 and m['mesh_peer_reachable{peer="b"}'] == 1
        assert m['mesh_peer_route_edges{peer="r"}'] == 3 and m['mesh_peer_route_edges{peer="b"}'] == 6
        assert m['mesh_peer_links{peer="r"}'] == 1 and m['mesh_peer_links{peer="b"}'] == 0
        assert 0 <= m['mesh_record_age_seconds{peer="b"}'] < 600, m
        assert m['mesh_record_vertices{peer="r"}'] >= 3 and m['mesh_record_links{peer="r"}'] == 2
        assert m['mesh_packets_received_total{kind="graph"}'] > 0
        assert m['mesh_packets_sent_total{kind="graph"}'] > 0
        assert m['mesh_packets_received_total{kind="data"}'] >= 5
        assert m['mesh_packets_sent_total{kind="data"}'] >= 5
        assert m['mesh_link_up_total'] == 1 and m['mesh_link_down_total'] == 0
        assert m['mesh_tun_read_total'] >= 5 and m['mesh_tun_delivered_total'] >= 5
        unrouted = m['mesh_tun_unrouted_total']
        assert m['mesh_records_applied_total'] >= 2
        assert m['mesh_vectors_applied_total'] >= 2, 'version vectors not exchanged'
        assert m['mesh_vectors_invalid_total'] == 0
        assert m['mesh_records_invalid_total'] == 0
        assert m['mesh_link_up_total'] == 1 and m['mesh_link_down_total'] == 0
        assert m['mesh_forward_dropped_total'] == 0
        assert sum(v for k, v in m.items() if k.startswith('mesh_packets_rejected_total')) == 0
        relay = metrics(lab, 'r')
        assert relay['mesh_tun_delivered_total'] == 0
        assert relay['mesh_packets_received_total{kind="data"}'] >= 10
        assert relay['mesh_peer_links{peer="a"}'] == 1 and relay['mesh_peer_links{peer="b"}'] == 1

        # b stops hearing r; its record drops the link and the route to b disappears upstream.
        lab.block('r', 'b', both=False)
        lab.wait(lambda: metrics(lab, 'a')['mesh_peer_reachable{peer="b"}'] == 0, 'route loss reaches the metrics')
        udp.send(b'unrouted')
        lab.wait(lambda: metrics(lab, 'a')['mesh_tun_unrouted_total'] > unrouted, 'unrouted packet counted')
        m = metrics(lab, 'b')
        assert m['mesh_link_down_total'] == 1 and m['mesh_links'] == 0
        assert m['mesh_peer_route_edges{peer="r"}'] == 3, 'the working direction lost its route'
        assert m['mesh_peer_links{peer="r"}'] == 0
        m = metrics(lab, 'r')
        assert m['mesh_links'] == 2 and 'mesh_peer_route_edges{peer="b"}' not in m
        assert m['mesh_events_queued'] >= 0
        lab.unblock('r', 'b', both=False)
        lab.wait_ping('a', 'b')
        lab.wait(lambda: metrics(lab, 'b')['mesh_link_up_total'] == 2, 'link restored')


lib.main(test)
