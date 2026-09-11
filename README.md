# mesh

[![CI](https://github.com/pg83/mesh/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/pg83/mesh/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/pg83/mesh/branch/master/graph/badge.svg)](https://app.codecov.io/gh/pg83/mesh)
[![Go version](https://img.shields.io/github/go-mod/go-version/pg83/mesh)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Private overlay network for a closed set of nodes. Every node knows every
public key; UDP and WS/WSS links use keys derived from those static keys and carry
authenticated encrypted packets; a gossip map on each node describes who can reach whom; the
source picks the whole path and relays only follow it; IP rides on top over a
TUN device.

This is the first version: registry, links, TUN, gossip, the routing map,
relaying, and periodic gossip over the full address closure. Link metrics,
and retransmits come later.

## Usage

```
mesh keygen                 # prints {"pub": ..., "key": ...}
mesh run -c config.json     # runs a node
mesh run -c config.json -key-file /home/pg/.ssh/home.key
mesh status -s status.sock  # dumps links as JSON
```

Config is JSON:

`-key-file` overrides `key` in the config, which may then be omitted. The file
contains either a base64-encoded 32-byte seed (surrounding whitespace is ignored)
or an unencrypted OpenSSH Ed25519 private key. An unreadable or invalid file
fails startup; it does not fall back to the config key. Encrypted SSH keys and
other SSH key types are not supported.

For an SSH identity, put the complete `ssh-ed25519 AAAA...` public key line in
the registry's `pub` field and omit `sig`. Mesh derives the X25519 public key
from the Ed25519 point and uses the Ed25519 public key for signatures. Existing
base64 `pub`/`sig` entries remain supported and can share the same registry.

```json
{
  "index": 1,
  "key": "<base64 private key>",
  "endpoint": [{"proto": "udp", "addr": "0.0.0.0", "port": 7000}],
  "subnet": "10.77.0.0/24",
  "status": "/run/mesh/status.sock",
  "registry": [
    {"index": 1, "pub": "<x25519>", "sig": "<ed25519>", "intip": "10.77.0.1", "endpoint": [{"proto": "udp", "addr": "203.0.113.10", "port": 17001, "bind_addr": "192.168.1.20", "bind_port": 7001}]},
    {"index": 2, "pub": "<x25519>", "sig": "<ed25519>", "intip": "10.77.0.2", "endpoint": []}
  ]
}
```

The registry is the same on every node: index, both public keys, internal
address, and the static endpoints a node has, if any. Node indexes must be
in the range 1–65535; zero is reserved and rejected in the registry.
A node without static
endpoints is never dialed by a node that has not heard of it; it dials, and
its own advertised addresses let others dial it back later. `tun` (default
`mesh0`) and `mtu` (default 1380) are optional.

Both the host and registry use the same flat `endpoint` objects. The node
opens the union of its host list and its own registry list. Other nodes
use only the advertised `addr` and `port` from that registry entry.
There is no global `port`, separate `static` list, or `forwards` section.
Old configurations must be converted to this format.

| Field | Meaning |
|---|---|
| `proto` | Transport: `udp`, `ws`, or `wss` |
| `addr`, `port` | Address and port advertised to peers |
| `bind_addr`, `bind_port` | Local address and port; omitted values default to `addr` and `port` |
| `path` | WebSocket request path including query, default `/` |

For UDP, `addr: "0.0.0.0"` expands to eligible IPv4 interface addresses
on this host, refreshed every second. A concrete address selects that
interface address. Loopback, link-local and mesh-subnet addresses are excluded.
Multiple entries can use the same local port. Each endpoint pair has a connected
UDP socket and its own receive queue; a listener on the same port handles
authenticated packets from previously unknown endpoints. Outgoing packets select
the configured source IP and interface. Reception accepts only configured
local address/port pairs. Each local pair must map to one advertised pair,
and each advertised pair to one local pair. Exact duplicate entries are harmless.

Each directed transport edge has a goroutine and a buffered mailbox. Edge actors
handle authentication, gossip and forwarding directly to the next edge actor;
inactive candidates remain available for rediscovery. One graph goroutine merges
observations and advertisements and periodically publishes a shared immutable
snapshot, including routes, to the actors and TUN. Mailbox sends are nonblocking:
full queues drop messages, and snapshots and link observations are repeated.
Socket reads run independently of mailbox processing. There is no shared mutex
around graph updates or packet forwarding.

The example above uses two different addresses and two different ports:

```text
203.0.113.10:17001  <->  192.168.1.20:7001
       public                 local
```

The router forwards inbound UDP to `192.168.1.20:7001` and translates
outbound packets from that pair to `203.0.113.10:17001`. Configure that
mapping on the router separately; mesh does not configure NAT. Correct
outbound translation matters too: peers must observe the advertised source
pair. The dedicated local port distinguishes this mapping from ordinary
LAN traffic on port 7000. The graph uses the public pair for this socket;
its private pair is used only to send and receive packets. To use the LAN
address directly too, keep the separate port-7000 entry shown above.

WS/WSS uses the same endpoint shape. Each connection carries both directions.
For native TLS, set `tls_cert` and `tls_key` on the local endpoint. The client
checks the certificate and advertised hostname/IP against system trust roots;
`tls_ca` on a registry endpoint adds a private CA bundle. TLS files are local
paths and are never advertised through gossip. Multiple paths can share a
TCP listener; different paths remain different endpoints.

For TLS termination at a reverse proxy, configure the public WSS endpoint
and explicitly select plaintext WS on the local binding:

```json
{"proto":"wss","addr":"mesh.example.net","port":443,"path":"/mesh",
 "bind_proto":"ws","bind_addr":"192.168.1.20","bind_port":8080}
```

The proxy must preserve the public HTTP Host and request path and support
WebSocket upgrades. `bind_proto` defaults to `proto`. UDP and TCP may use the
same port. WS and native WSS need different TCP ports. Mesh authenticates
and encrypts its packets even when TLS terminates at a proxy.

The TUN interface persists across daemon exits, so a restart does not remove
the application's local address and route. The next process reattaches to
that interface. When changing the configured TUN name or removing mesh,
remove the old interface explicitly with `ip link del <name>`.

`key` is one 32-byte seed. The X25519 static keypair (`pub`) and the
advertisement signing keypair (`sig`) are both derived from it: an
advertisement travels past its author, so the link cipher cannot vouch for
it and it carries its own signature.

## Wire format

All multibyte integers in the mesh protocol use little-endian order.
Encapsulated IP packets retain their standard network format. The key
context is `mesh/6`; older wire formats are incompatible.

Each registered pair derives a shared secret with X25519 and directional
keys with HKDF-SHA256. The context contains `mesh/6`, the sender's public key
and the receiver's public key. There is no handshake or forward secrecy.

| Type | Layout |
|---|---|
| data transport | `3`, sender index (2), packet ID (8), random nonce (24), XChaCha20-Poly1305 ciphertext and tag (16) |
| gossip transport | `4`, the same remaining header and encryption |

The header is authenticated as associated data. Every packet gets a fresh
random nonce, including after a process restart.

Inner data starts with `1`, hop count (1), cursor (1), then the route. Each
hop is a pair of endpoints: source endpoint hash (8), destination endpoint hash (8). The original IP packet follows. At most 16 transport
hops are allowed. A relay checks the receiving pair against the route,
advances the cursor, and sends from the exact next source endpoint to the
exact next destination. Local delivery verifies the destination mesh IP.

Inner gossip starts with `2`, an Ed25519 signature (64), and a JSON object
containing `index`, `endpoints`, and `edges`. Endpoint descriptions contain
`proto`, `addr`, `port`, and optional `path`. Edges contain `from` and `to`
64-bit hashes, `id`, and `alive`. Descriptions appear once per message;
each message includes the descriptions referenced by its edges. Publications
split at eight records or roughly 1000 JSON bytes; a single large endpoint
record can exceed that target. There is no dependency on an earlier gossip
message arriving first. Other nodes can use these descriptions to open new
direct connections.

The signer may transmit any part of the graph, including records learned
from other members. An omitted pair is unchanged; a newer record replaces
an older one. Gossip remains periodic, once per second.

A WebSocket binary message contains one mesh transport packet. The first
packet on a connection contains inner type `3` followed by a JSON object
with the source and destination endpoint descriptions. Both ends validate
this binding with the existing derived keys; no new session keys are negotiated.
Subsequent messages carry the existing data and gossip packets. The receiving
endpoint comes from this authenticated binding, not the proxy's TCP address.

The graph owner and each outgoing edge have separate counters initialized
from Unix nanoseconds. The graph counter versions local records; an edge's
counter identifies its outgoing transport packets and WebSocket attempts.
Forwarding a graph record preserves its ID and alive flag.

## Map and routing

Endpoints live in `map[uint64]Endpoint`; the graph key is a pair of uint64
hashes and the value contains only record ID and alive state. Routes carry
the same hashes. Full addresses are resolved only by the local transport.
The hash is the first eight SHA-256 bytes interpreted as a little-endian
uint64, over `proto + NUL + addr + NUL + decimal-port + NUL + path`.
Hostnames are lowercased, IPs normalized, and an empty WS path becomes `/`.
UDP path is empty. Local bind and TLS settings are excluded. Conflicting
descriptions with the same hash fail explicitly.

The graph remains a map of directed endpoint pairs;
registry indexes identify encryption keys, not graph vertices. An internal
mesh address is represented as `(meshIP, 0)`. Each host supplies both edges
between that vertex and each of its advertised transport endpoints with an active local binding. It withdraws
its obsolete local attachments, including those learned after a restart. Static
registry addresses are discovery candidates, not evidence of a live edge.

Receiving an authenticated packet observes precisely its UDP
source and destination pair. Connected sockets identify the pair directly;
discovery uses socket packet metadata. The local address is translated to the
configured public pair for a forwarded endpoint. That incoming edge remains locally alive while packets arrive,
and is withdrawn after five seconds of silence. The reverse edge is
independent. Any accepted data or gossip packet refreshes the observation.

Every second each host sends its known graph through all combinations of
local endpoints and candidate remote endpoints. Candidates come from the
registry, graph endpoint ownership, and authenticated source addresses.
Learned addresses within the mesh subnet are filtered. Sending uses the
selected source address and interface through UDP socket control metadata.
Discovery does not depend on an existing route or a reverse connection.

Gossip merges each directed pair independently. An omitted pair is unchanged;
a newer record replaces an older version of that pair. Receiving gossip updates
the graph without sending a reply or forwarding it immediately. The next
one-second publication includes the updated graph.
Records have no age-based expiry. A newer `alive=false` record withdraws an
edge; older versions cannot restore it. Automatic cleanup of unreachable
parts of the graph is not implemented.
Local observations generate fresh versions each second.

BFS follows the directed endpoint graph, with stable endpoint ordering for
identical path lengths. The resulting path is compiled into concrete transport
hops; movements between endpoints on the same host require no packet. The
return path is computed independently. There is no separate per-host
endpoint selector. Graph changes and the one-second local observation pass
rebuild routes.

Status exposes incoming endpoint pairs, the live graph, its vertices, and
routes keyed by destination endpoint. Each edge reports its latest observation
periodically; the graph owner publishes immutable snapshots through the same
bounded mailboxes used for packets.
WS connections are indexed by an unordered endpoint pair, so incoming and
outgoing routes use one connection. Simultaneous dials prefer the connection
initiated by the smaller endpoint hash; duplicate attempts from that same
endpoint prefer the larger first-packet ID. A sole working connection stays
open regardless of initiator. There is at most one pending dial per pair,
and a new timer tick never restarts that attempt. Each WS connection has a
reader and writer, with a bounded outgoing queue; a full queue drops packets
rather than blocking other transports. Dial, HTTP upgrade, authentication
exchange, and socket writes run independently of graph updates. Old connection
cleanup cannot remove its replacement. Five seconds of silence withdraws
incoming liveness independently of TCP connection state.
Status also shows selected WS connections (pair, initiator, first-packet ID)
and the number of pending dials. Crypto keys stay
shared across a peer's endpoints and survive local link expiry. Transport duplicate detection is not implemented.

## Development

```
./build          # .build/bin/mesh, published as ./mesh
./build test     # e2e topologies in tst/
./lint.sh        # house style gate, needs a sibling ay checkout
```

CI runs the same `./build test` on every push and pull request. The lab needs
unprivileged user namespaces and the tun module, which the workflow enables.
The separate Race detector job runs the entire suite, including QUIC stress,
with `./build -j 4 -Drace test`. This builds mesh with `-race` and CGO enabled
(a C compiler is required). A detected race immediately fails the process;
CI preserves its report with the test logs. CLI invocations skip the race
runtime's one-second exit delay.

Coverage comes from the end-to-end suite rather than from unit tests:
`./build -Dcoverage coverage` builds an instrumented binary, points every test
node at its own `GOCOVERDIR`, and merges the counters every mesh process wrote
at exit into `.build/coverage.out`. CI uploads that profile to Codecov.

E2E tests run nodes in separate network namespaces wired by a userspace
switch (`tst/lib.py`); see `CLAUDE.md`.

## License

MIT. See [LICENSE](LICENSE).

## Application and failure tests

The e2e suite requires Linux, Python 3.12+, Go, iproute2, util-linux, OpenSSH
(client and server), rsync, curl and iperf3. All are required in CI; missing
programs fail the test. Each application runs in a node's network namespace
and connects to another node's mesh IP. OpenSSH runs as the invoking user
inside a nested user namespace, with fresh host/user keys and no PAM.

`Lab.block` / `unblock` cut individual directions or segments without killing
nodes. `intercept` can drop, delay, hold, duplicate, copy or corrupt selected
outer packets. Held packets can be released later; delivery counters show
which path carried traffic. These controls live in the existing userspace
switch, not in the mesh daemon.

The application scenarios exercise persistent SSH across direct/relay path
changes, one-way failures, relay exits, total outages, mesh restarts, endpoint
roaming and fallback between two physical segments. Concurrent SSH clients
have distinct mesh IPs. The same SSH process exchanges numbered requests
throughout; tests reject missing/duplicate replies and report its maximum
pause. Route convergence is bounded by 30 seconds and SSH recovery by 60
seconds, allowing for gossip propagation and TCP retries after the five-second link timeout.

The QUIC stress test runs one server and four clients in five separate
namespaces. All clients start together and continuously exchange 64 KiB
blocks for 30 seconds, verifying every byte. It checks four distinct source
IPs, one connection per client, matching server/client byte counts, and
prints each client's throughput. `mesh-quic` uses
[quic-go](https://github.com/quic-go/quic-go) and is built only with the
`meshquic` tag; it is absent from the production binary and coverage profile.
The stress test runs in all three CI jobs as part of the regular e2e suite.

Other scenarios transfer and hash files through scp and curl while cutting
the active path, synchronize trees with rsync, and run iperf3 TCP/UDP streams
with deterministic loss and delay. UDP probes check packet sizes, reordering
across busy channels, endpoint binding and ciphertext authentication. Separate tests check
gossip on every endpoint during UDP traffic, data keeping a link alive
when gossip is dropped, five-second local link expiry, and a
20-second RTT carrying UDP traffic without link flaps. Gossip reconnection, unknown
keys, CLI errors and malformed packets also have separate tests. `mesh-probe` is built only with the `meshprobe` tag for the
protocol test; it sends authenticated malformed messages to real mesh nodes.
It is absent from the production binary and its coverage profile.

`./build -j 4 test` runs the suite. `./build -j 4 -Dcoverage coverage` runs it
against the instrumented daemon and enforces 95% statement coverage. Each
individual daemon run has a separate counter directory; shutdown waits for
all processes and missing daemon counters fail the run. CLI coverage is
merged too. Codecov receives that same profile and requires 95% coverage.

NAT scenarios translate both IPv4 addresses and UDP ports in the test router.
They exercise two isolated private networks, failure of one forwarded port,
and the same SSH and QUIC connections migrating from LAN to a public endpoint
and then to a second forwarded port. These tests do not verify a physical Xiaomi router.

WS tests force simultaneous TCP SYNs, count established kernel sockets,
verify bidirectional traffic through an accepted connection, distinguish paths
on a shared listener, reject unauthenticated or misbound connections, and
exercise native WSS trust checks and a real TLS reverse proxy. A slow writer
must not block status or another UDP peer. SSH and QUIC migrate UDP/WSS/UDP
without reconnecting their application processes.

The additional twenty protocol and application scenarios are listed in
[tst/SCENARIOS.md](tst/SCENARIOS.md).

On failure the suite prints application/mesh logs and channel counters. Set
`MESH_TEST_ARTIFACTS` to preserve these along with status snapshots outside
the build temporary directory; CI uploads them as failure artifacts.
