package main

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"net"
	"slices"
	"strconv"
	"sync/atomic"
	"time"
)

var (
	packetKinds   = [...]string{"data", "graph", "registry", "versions"}
	rejectReasons = [...]string{"short", "header", "auth", "source", "replay"}
)

const (
	rejectShort = iota
	rejectHeader
	rejectAuth
	rejectSource
	rejectReplay
)

type Metrics struct {
	received, sent                               [len(packetKinds)]atomic.Uint64
	receivedBytes, sentBytes                     atomic.Uint64
	rejected                                     [len(rejectReasons)]atomic.Uint64
	recordsApplied, recordsStale, recordsInvalid atomic.Uint64
	vectorsApplied, vectorsStale, vectorsInvalid atomic.Uint64
	forwardNoChannel                             atomic.Uint64
	tunRead, tunUnrouted, tunDelivered           atomic.Uint64
	tunDropped, tunExit                          atomic.Uint64
	linkUp, linkDown, dialFailed                 atomic.Uint64
}

type MetricsWriter struct {
	out io.Writer
}

func (w *MetricsWriter) family(name, kind, help string) {
	fmt.Fprintf(w.out, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
}

func (w *MetricsWriter) value(name string, labels [][2]string, value float64) {
	fmt.Fprint(w.out, name)

	for i, label := range labels {
		separator := "{"

		if i != 0 {
			separator = ","
		}

		fmt.Fprintf(w.out, "%s%s=%s", separator, label[0], strconv.Quote(label[1]))
	}

	if len(labels) != 0 {
		fmt.Fprint(w.out, "}")
	}

	fmt.Fprintf(w.out, " %s\n", strconv.FormatFloat(value, 'g', -1, 64))
}

func (w *MetricsWriter) counter(name, help string, value *atomic.Uint64) {
	w.family(name, "counter", help)
	w.value(name, nil, float64(value.Load()))
}

func (w *MetricsWriter) gauge(name, help string, value float64) {
	w.family(name, "gauge", help)
	w.value(name, nil, value)
}

func btoi(value bool) int {
	if value {
		return 1
	}

	return 0
}

func peerName(peer PeerConfig) string {
	return cmp.Or(peer.Name, strconv.Itoa(int(peer.Index)))
}

func writeMetrics(out io.Writer, st *Status, m *Metrics, queued int, now time.Time) {
	w := &MetricsWriter{out: out}

	w.family("mesh_packets_received_total", "counter", "Authenticated packets received on incoming channels by inner kind.")

	for i, kind := range packetKinds {
		w.value("mesh_packets_received_total", [][2]string{{"kind", kind}}, float64(m.received[i].Load()))
	}

	w.family("mesh_packets_sent_total", "counter", "Packets queued to outgoing channels by inner kind.")

	for i, kind := range packetKinds {
		w.value("mesh_packets_sent_total", [][2]string{{"kind", kind}}, float64(m.sent[i].Load()))
	}

	w.counter("mesh_bytes_received_total", "Transport bytes received on incoming channels.", &m.receivedBytes)
	w.counter("mesh_bytes_sent_total", "Transport bytes queued to outgoing channels.", &m.sentBytes)
	w.family("mesh_packets_rejected_total", "counter", "Packets dropped by an incoming channel by reason.")

	for i, reason := range rejectReasons {
		w.value("mesh_packets_rejected_total", [][2]string{{"reason", reason}}, float64(m.rejected[i].Load()))
	}

	w.counter("mesh_records_applied_total", "Graph records newer than the stored version.", &m.recordsApplied)
	w.counter("mesh_records_stale_total", "Graph records not newer than the stored version.", &m.recordsStale)
	w.counter("mesh_records_invalid_total", "Graph records rejected by the decoder.", &m.recordsInvalid)
	w.counter("mesh_vectors_applied_total", "Version vectors newer than the stored version.", &m.vectorsApplied)
	w.counter("mesh_vectors_stale_total", "Version vectors not newer than the stored version.", &m.vectorsStale)
	w.counter("mesh_vectors_invalid_total", "Version bundles rejected by the decoder.", &m.vectorsInvalid)
	w.counter("mesh_forward_dropped_total", "Data packets dropped by a relay without a channel for the next hop.", &m.forwardNoChannel)
	w.counter("mesh_tun_read_total", "IP packets read from the TUN device.", &m.tunRead)
	w.counter("mesh_tun_unrouted_total", "IP packets from the TUN device without a route.", &m.tunUnrouted)
	w.counter("mesh_tun_dropped_total", "IP packets for the local system dropped for lack of a TUN device.", &m.tunDropped)
	w.counter("mesh_tun_exit_total", "IP packets from the TUN device sent to an exit node.", &m.tunExit)
	w.counter("mesh_tun_delivered_total", "IP packets written to the TUN device.", &m.tunDelivered)
	w.counter("mesh_link_up_total", "Incoming links observed for the first time.", &m.linkUp)
	w.counter("mesh_link_down_total", "Incoming links expired or closed.", &m.linkDown)
	w.counter("mesh_dial_failed_total", "Failed outgoing channel attempts.", &m.dialFailed)
	w.gauge("mesh_up", "The node answers on its control address.", 1)
	w.gauge("mesh_graph_edges", "Directed edges in the graph.", float64(len(st.Graph)))
	w.gauge("mesh_graph_vertices", "Vertices with at least one edge.", float64(len(st.Vertices)))
	w.gauge("mesh_addresses", "Vertices in the address dictionary.", float64(len(st.Addresses)))
	w.gauge("mesh_records", "Graph records held, including the local one.", float64(len(st.Records)))
	w.gauge("mesh_links", "Incoming links observed within the last five seconds.", float64(len(st.Links)))
	w.gauge("mesh_dials_pending", "Outgoing channel attempts in progress.", float64(st.Dialing))
	w.gauge("mesh_routes", "Peers with a computed route.", float64(len(st.Routes)))
	w.gauge("mesh_events_queued", "Messages waiting for the graph actor.", float64(queued))
	w.family("mesh_channels", "gauge", "Channels by transport and direction.")

	channels := map[[2]string]int{}

	for _, c := range st.Channels {
		direction := "incoming"

		if c.Outgoing {
			direction = "outgoing"
		}

		channels[[2]string{c.Transport, direction}]++
	}

	for _, key := range slices.SortedFunc(maps.Keys(channels), func(a, b [2]string) int { return slices.Compare(a[:], b[:]) }) {
		w.value("mesh_channels", [][2]string{{"transport", key[0]}, {"direction", key[1]}}, float64(channels[key]))
	}

	incoming := map[uint16]int{}

	for _, link := range st.Links {
		incoming[vertexOwner(link.From)]++
	}

	peers := []PeerConfig{}

	for _, record := range st.Registry {
		if record.Index != st.Index {
			peers = append(peers, record.PeerConfig)
		}
	}

	alive := map[uint16]bool{}

	for _, index := range st.Alive {
		alive[index] = true
	}

	w.family("mesh_peer_alive", "gauge", "Whether some node observes a link from the peer.")

	for _, peer := range peers {
		w.value("mesh_peer_alive", [][2]string{{"peer", peerName(peer)}}, float64(btoi(alive[peer.Index])))
	}

	w.family("mesh_peer_reachable", "gauge", "Whether a route to the peer's mesh address exists.")

	for _, peer := range peers {
		route := st.Routes[udpVertex(net.ParseIP(peer.Intip), 0).string()]
		reachable := 0.0

		if len(route) != 0 {
			reachable = 1
		}

		w.value("mesh_peer_reachable", [][2]string{{"peer", peerName(peer)}}, reachable)
	}

	w.family("mesh_peer_route_edges", "gauge", "Edges on the route to the peer's mesh address.")

	for _, peer := range peers {
		if route := st.Routes[udpVertex(net.ParseIP(peer.Intip), 0).string()]; len(route) != 0 {
			w.value("mesh_peer_route_edges", [][2]string{{"peer", peerName(peer)}}, float64(len(route)))
		}
	}

	w.family("mesh_peer_links", "gauge", "Incoming links from the peer's sockets.")

	for _, peer := range peers {
		w.value("mesh_peer_links", [][2]string{{"peer", peerName(peer)}}, float64(incoming[peer.Index]))
	}

	names := map[uint16]string{}

	for _, peer := range peers {
		names[peer.Index] = peerName(peer)
	}

	foreign := []*GraphRecord{}

	for _, record := range st.Records {
		if record.Owner != st.Index {
			foreign = append(foreign, record)
		}
	}

	w.family("mesh_record_age_seconds", "gauge", "Time since a newer graph record of the peer was applied.")

	for _, record := range foreign {
		w.value("mesh_record_age_seconds", [][2]string{{"peer", names[record.Owner]}}, now.Sub(record.applied).Seconds())
	}

	w.family("mesh_record_vertices", "gauge", "Vertices in the peer's graph record.")

	for _, record := range foreign {
		w.value("mesh_record_vertices", [][2]string{{"peer", names[record.Owner]}}, float64(len(record.Vertices)))
	}

	w.family("mesh_record_links", "gauge", "Links in the peer's graph record.")

	for _, record := range foreign {
		w.value("mesh_record_links", [][2]string{{"peer", names[record.Owner]}}, float64(len(record.Links)))
	}
}
