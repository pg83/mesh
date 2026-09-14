The control server gains `GET /metrics` in Prometheus text format: packet, byte, rejection, record, forwarding, TUN, link and dial counters kept inside the node, plus a snapshot of graph, channel and per-peer gauges such as reachability, route length, incoming links and record age. Scrape the control address directly.

Unreachable guards were removed and the test suite now covers frame checks on established WebSocket channels, channel replacement, malformed routes and concrete UDP listeners following their address.

This release uses mesh/13 and the release 18 graph format. Update peers together with releases before 18.
