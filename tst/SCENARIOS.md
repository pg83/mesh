# Additional protocol and application scenarios

All twenty scenarios run in the normal, race and coverage e2e jobs.
Network faults are injected by the lab switch; applications are real processes
inside the nodes' network namespaces.

| # | Test | Check |
|---|---|---|
| 1 | endpoint_oneway | Working endpoint alongside an incoming-only endpoint |
| 2 | endpoint_stale | Delayed packets cannot revert a source address |
| 3 | quic_failover | Four loaded connections migrate to a relay and back |
| 4 | quic_mtu | Existing connection moves to a smaller physical MTU |
| 5 | quic_isolation | Congested bulk traffic does not starve an independent SSH channel |
| 6 | quic_slow_reader | One flow-controlled client does not stop three others |
| 7 | quic_restart | Busy sender restart with the same QUIC connection |
| 8 | restart_all | Simultaneous mesh restart preserves an existing SSH application |
| 9 | partition_merge | Independent topology changes converge after partitions merge |
| 10 | route_stale | Delayed gossip and an obsolete in-flight source route |
| 11 | route_asymmetric | Different forward and return paths with reverse directions blocked |
| 12 | gossip_cycles | Bounded flooding despite cycles and duplicate delivery |
| 13 | gossip_stale | A superseded record cannot restore a withdrawn vertex |
| 14 | gossip_lost_withdrawal | Periodic gossip delivers missed withdrawals while data keeps its incoming link alive |
| 15 | endpoint_fanout | Working endpoint after 32 unreachable candidates |
| 16 | packet_header_auth | Changing the transport ID invalidates authentication |
| 17 | packet_binding | Reflection and cross-peer ciphertext injection |
| — | ws_source | Every first message type carries the actual client socket, works without a reply, and preserves endpoint metadata |
| 18 | packet_endpoint_binding | A packet must arrive on the endpoint pair in its source route |
| 19 | quic_noise | SSH and QUIC survive a bounded stream of invalid UDP packets |
| 20 | route_limit | Full-size data at 16 hops; no route at 17 hops |

The endpoint graph refactor also checks two source IPs sharing one destination,
SSH migration after a UDP port change, and third-party graph batches with
independent pair updates and explicit withdrawals across relays.
