# mesh

[![CI](https://github.com/pg83/mesh/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/pg83/mesh/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/pg83/mesh/branch/master/graph/badge.svg)](https://app.codecov.io/gh/pg83/mesh/tree/master)
[![Go version](https://img.shields.io/github/go-mod/go-version/pg83/mesh)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Private overlay network for a closed set of nodes. Nodes exchange their
public-key registries; UDP and WS/WSS links use keys derived from those keys and carry
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
mesh run -c config.json -key-file /path/to/private-key
mesh status -control 127.0.0.1:8058
mesh web -control 127.0.0.1:8058 -listen 127.0.0.1:8059
```

Config is JSON:

`-key-file` overrides `key` in the config, which may then be omitted. The file
contains either a base64-encoded 32-byte seed (surrounding whitespace is ignored)
or an unencrypted OpenSSH Ed25519 private key. An unreadable or invalid file
fails startup; it does not fall back to the config key. Encrypted SSH keys and
other SSH key types are not supported.

For an SSH identity, put the complete `ssh-ed25519 AAAA...` public key line in
the registry's `pub` field. Mesh derives the X25519 public key from the Ed25519
point. Base64 X25519 `pub` entries can share the same registry. Legacy `sig`
fields are ignored.

```json
{
  "index": 1,
  "key": "<base64 private key>",
  "endpoint": [
    {"proto": "udp", "addr": "0.0.0.0", "port": 7000},
    {"proto": "udp", "addr": "::", "port": 7000}
  ],
  "subnet": "10.77.0.0/24",
  "control": "127.0.0.1:8058",
  "registry": [
    {"index": 1, "pub": "<x25519>", "intip": "10.77.0.1", "endpoint": [{"proto": "udp", "addr": "203.0.113.10", "port": 17001, "bind_addr": "192.168.1.20", "bind_port": 7001}]},
    {"index": 2, "pub": "<x25519>", "intip": "10.77.0.2", "endpoint": []}
  ]
}
```

The initial registry contains the local node and its known peers: index,
public key, internal address, and static endpoints, if any. Only the bootstrap
hosts need the complete registry; other nodes can start with their own entry
and those hosts. TUN and discovery start immediately from this local configuration.
Node indexes must be
in the range 1–65535; zero is reserved and rejected in the registry.
A node without static
endpoints is never dialed by a node that has not heard of it; it dials, and
its own advertised addresses let others dial it back later. `tun` (default
`mesh0` on Linux, `utun` on macOS) and `mtu` (default 1380) are optional.

`registry_version` sets the version of every record loaded from the config;
it defaults to `1`. Set a higher version in the authoritative configuration
when changing its registry. Versions are not derived from startup or send time.
Every five seconds each outgoing edge sends a separate registry message with
a random selection of known records fitting in one encrypted message. The
record budget reserves the full IPv6 source header and keeps UDP messages
within 1200 bytes; WS messages include their variable-length source endpoint.
Learned records retain their versions and are retransmitted too.
An unknown index is added; an existing record changes only for a strictly higher
version. There are no deletions, expiry, whole-registry replacement or fragments.
Records too large for one packet are rejected at startup.

Registry messages use the existing authenticated peer sessions, with their own
outer packet type `5` and inner type `4`. They can traverse the mesh by repeated
exchange between neighbors. Trusted peers can introduce other peers; there is
no separate origin signature. Registry changes publish immutable actor snapshots,
and a public-key change replaces the affected sessions. The running node's own
key and mesh IP stay fixed by its local configuration.

Both the host and registry use the same flat `endpoint` objects. Endpoints describe incoming listeners only. The node
opens the union of its host list and its own registry list. An empty list is valid.
UDP, WS and WSS dialing is always available independently of these listeners. Other nodes
use only the advertised `addr` and `port` from that registry entry.
There is no global `port`, separate `static` list, or `forwards` section.
Old configurations must be converted to this format.

`no_dial` is an optional local list of directed IP pairs, for example
`[{"from":"10.0.0.64","to":"10.0.0.68"}]`. A pair disables initiating
outgoing channels from that local IP to endpoints with that literal destination IP,
regardless of port or transport. IPv4 and IPv6 addresses are matched exactly;
there are no subnets or DNS matching. Incoming channels are allowed. An accepted WS connection can provide a separate
return channel; receiving UDP never creates one. To stop both nodes initiating, configure each direction
on its source node. These rules are not exchanged in the registry or included
in exported configurations.

| Field | Meaning |
|---|---|
| `proto` | Transport: `udp`, `ws`, or `wss` |
| `addr`, `port` | Address and port advertised to peers |
| `bind_addr`, `bind_port` | Local address and port; omitted values default to `addr` and `port` |
| `path` | WebSocket request path including query, default `/` |

For UDP, `addr: "0.0.0.0"` expands to eligible IPv4 interface addresses;
`addr: "::"` expands to global or ULA IPv6 addresses. Include both for dual stack.
Link-local addresses are excluded because their scope is local to an interface.
An independent interface actor scans at startup, on OS address/link events
(Linux netlink, Darwin routing socket), and every 30 seconds as a fallback.
The graph receives an immutable address list; interface enumeration errors
do not terminate mesh. Explicit binds listen on exactly that address. A concrete
bind waits while its address is absent, appears when the address is added, and
closes when the address disappears. Loopback binds are allowed explicitly;
wildcards do not advertise loopback. Link-local and mesh-subnet addresses are excluded.
Multiple paths or duplicate declarations can share an explicitly configured listener.
Overlapping UDP wildcard and explicit binds share one socket per address family and port.

Outgoing channels are created for eligible interface addresses and known remote
endpoints. The OS assigns their local UDP/TCP port. UDP never reuses a configured
listener's port for outgoing traffic. The graph uses the actual local socket
address and port, not an interface placeholder. A new port on reconnect creates
a new vertex; closing the old channels drops them from the node's graph record. A vertex's
`endpoint` flag explicitly says whether it accepts new channels. Client socket
vertices and mesh-IP:0 vertices have this flag cleared.

All transport traffic goes through a directed `Channel` with its own protocol actor.
UDP has no connection abstraction: an outgoing channel writes datagrams from an
unconnected socket; a listening socket dispatches datagrams to receiving channels.
Every authenticated message includes its full source vertex. The receiving
socket supplies the destination endpoint. Any ordinary data, vertex, edge or
registry message can be the first one; there is no preliminary message or reply.
The receiver sends no gossip, registry or data back through that UDP channel.
Return traffic requires an independently created outgoing channel to a known listener,
or another route through the mesh. UDP-only nodes need a reachable advertised listener
for return traffic; WS/WSS clients can receive through an outgoing connection without one.

Each Channel has a goroutine and an unbounded FIFO mailbox. Channel actors
handle authentication, gossip and forwarding directly to the next channel;
connection attempts are tracked separately from graph edges and channels.
Channel actors and their mailboxes exit when their I/O closes. One graph goroutine merges
observations and advertisements and periodically publishes a shared immutable
snapshot, including routes, to the actors and TUN. Each mailbox has a channel-driven
queue that accepts messages independently of its consumer. Packets and snapshots
share that queue; there is no configured capacity or drop-on-full policy.
Socket reads run independently of mailbox processing. There is no shared mutex
around graph updates or packet forwarding.

The example above uses two different addresses and two different ports:

```text
203.0.113.10:17001  <->  192.168.1.20:7001
       public                 local
```

The router forwards inbound UDP to `192.168.1.20:7001` and translates
replies from that pair to `203.0.113.10:17001`. Configure that
mapping on the router separately; mesh does not configure NAT. Outgoing channels use ordinary dynamic NAT mappings and ephemeral ports.
The graph uses the public endpoint for the receiving side; its private pair is
used to receive the forwarded datagrams. To use the LAN
address directly too, keep the separate port-7000 entry shown above.

WS/WSS uses the same endpoint shape. Each connection backs two independent directed channels.
For native TLS, set `tls_cert` and `tls_key` on the local endpoint. The client
checks the certificate and advertised hostname/IP against system trust roots;
`tls_ca` on a registry endpoint adds a private CA bundle. TLS files are local
paths and are never advertised through gossip. Multiple paths can share a
TCP listener; different paths remain different endpoints.

For TLS termination at a reverse proxy, configure the public WSS endpoint
and explicitly select plaintext WS on the local binding:

```json
{"proto":"wss","addr":"mesh.example.net","port":443,"path":"/mesh",
 "bind_proto":"ws","bind_addr":"127.0.0.1","bind_port":8080}
```

The proxy must preserve the public HTTP Host and request path and support
WebSocket upgrades. `bind_proto` defaults to `proto`. UDP and TCP may use the
same port. WS and native WSS need different TCP ports. Mesh authenticates
and encrypts its packets even when TLS terminates at a proxy.

On Linux, the TUN interface persists across daemon exits, so a restart does not remove
the application's local address and route. The next process reattaches to
that interface. When changing the configured TUN name or removing mesh,
remove the old interface explicitly with `ip link del <name>`.
On macOS, closing the process's utun descriptor removes the interface and route.

`key` is one 32-byte seed from which the X25519 static keypair (`pub`) is
derived. Every transport packet, including gossip, is authenticated by the
link cipher. Nodes relay every node's graph record unchanged and keep only
the newest version; there is no separate advertisement signature or signing key.

## Wire format

All multibyte integers in the mesh protocol use little-endian order.
Encapsulated IP packets retain their standard network format. The key
context is `mesh/13`; upgrade all peers together. Releases through 14 use earlier channel semantics or wire formats and cannot exchange traffic with this
version. Update peers together.

Each registered pair derives a shared secret with X25519 and directional
keys with HKDF-SHA256. The context contains `mesh/13`, the sender's public key
and the receiver's public key. There is no handshake or forward secrecy.

| Type | Layout |
|---|---|
| data transport | `3`, sender index (2), packet ID (8), random nonce (24), XChaCha20-Poly1305 ciphertext and tag (16) |
| graph transport | `4`, the same remaining header and encryption |
| registry transport | `5`, the same remaining header and encryption |

The header is authenticated as associated data. Every packet gets a fresh
random nonce, including after a process restart. The encrypted plaintext is
`source vertex description || inner message`. Every transport packet contains
its complete source address, including the real port. A relay wraps the inner
message with its outgoing channel source; it preserves the route and payload
and advances the route cursor.

Inner data starts with `1`, edge count (1), cursor (1), then the route. Each
edge is a pair of vertices: source vertex hash (8), destination vertex hash (8). The opaque payload follows. The complete route includes the source and destination
mesh vertices and all local attachment edges. Routes allow at most 16 network
hops and 48 total edges. Disconnected, zero-length and malformed paths are rejected.
A relay checks its receiving channel against the current edge, follows the explicit
local edges, and sends through the next outgoing channel. Local edges must be
currently available. Only a path ending at the node's TUN vertex delivers to TUN.
The transport never reads the payload to find an address or choose a destination.
The TUN adapter handles IPv4/IPv6 packet framing and destination lookup on ingress;
node address allocation in the current registry remains IPv4.

The graph is a set of per-node records. A node's record holds its listener and
client socket vertices, each with its attachment directions, and the links it
observes into them. One record travels in one packet; every outgoing channel
sends every known record once per second.

Inner graph records start with `6`, owner registry index (2), version (8),
vertex count (2), the vertices, link count (2), and the links. Each vertex is
one flags byte (bit 0: `vertex -> meshIP`, bit 1: `meshIP -> vertex`, zero is
invalid) followed by its description. Each link is 10 bytes: the source vertex
hash (8) of another node's socket or listener and the index (2) of the local
destination vertex in this record. Endpoint descriptions start with kind (1: UDP/IPv4=1, WS=2, WSS=3,
UDP/IPv6=4, TCP/IPv4=5, TCP/IPv6=6) and port (2). The high bit of kind is
the `endpoint` flag; the low seven bits identify the transport/address format.
UDP/TCP then carry four IPv4 or sixteen IPv6 octets.
WS/WSS carry the address and path
as two strings, each prefixed by its byte length (2). Strings use UTF-8.

Counts and lengths are checked against the remaining packet before allocating
or reading. Truncated packets, unknown protocol codes, invalid flags, link
indexes outside the record, self-links, and trailing bytes reject the record as
a whole. A record with an unknown owner, the receiver's own index, or a version
not above the stored one is ignored. Lost or reordered records are repaired by
the next periodic publication. Other nodes can use vertices with
`endpoint: true` to open new direct channels. The flag is metadata and does not
change the address hash.

The authenticated sender relays every record it knows, including those of
other members, unchanged. A newer record replaces the owner's previous record
completely. Gossip remains periodic, once per second.

A WebSocket binary message contains one mesh transport packet with its source
address inside the authenticated ciphertext. The client side is the real TCP
local IP and port; the server side is the configured WS/WSS endpoint, including
its public hostname and path behind a proxy. The HTTP Upgrade selects that
endpoint. No mesh negotiation, binding packet or binding reply is sent.
The first ordinary message is processed immediately and identifies the peer
and source vertex for the server's two independent channels. A client socket
can receive through the existing connection without becoming a listener.

The graph owner and each outgoing edge have separate counters initialized
from Unix nanoseconds. The graph counter versions the local record, advancing
only when the record's content changes; an edge's counter identifies its
outgoing transport packets and WebSocket attempts. Forwarding a record
preserves its owner and version.

## Map and routing

Vertices live in `map[uint64]Vertex`, rebuilt from the registry, the records
and the local channels; the edge set is derived from the records. Routes carry
the same hashes. Full addresses are resolved only by the local transport.
The hash is the first eight SHA-256 bytes interpreted as a little-endian
uint64, over `proto + NUL + addr + NUL + decimal-port + NUL + path`.
Hostnames are lowercased, IPs normalized, and an empty WS path becomes `/`.
UDP/TCP paths are empty. Local bind, TLS settings and the endpoint flag are excluded. Conflicting
descriptions with the same hash fail explicitly.

The graph remains a map of directed endpoint pairs;
registry indexes identify encryption keys, not graph vertices. An internal
mesh address is represented as `(meshIP, 0)`. Local graph edges describe actual input/output directions. An open listener
advertises `endpoint -> meshIP` before accepting any peer. An outgoing channel
adds `meshIP -> local source`; an incoming channel adds `local destination -> meshIP`.
The graph owner takes the union of these capabilities, so closing one channel
cannot withdraw a direction still provided by another channel or listener.
A WS return channel can add `meshIP -> listener` on the server and
`source -> meshIP` on the client. Neither direction is implied by the address or
transport name. A restarted node publishes a record without its old sockets,
so their edges disappear everywhere once the record arrives. Static registry
addresses are discovery candidates, not evidence of a live network edge.

Receiving an authenticated packet observes precisely its directed channel.
UDP discovery obtains the local destination from socket packet metadata and
the configured listener mapping. Packets carry their source address in the
authenticated envelope and are dispatched to the corresponding channel.
The sender-provided source stays the same across NAT and reverse proxies. A forwarded listener uses its configured public endpoint
in the graph. The observed link expires after five seconds without packets;
closing its channel removes it from the record without inventing the reverse link.
A link only becomes an edge while its source vertex is in the current record of
its owner.

Every second each host sends its known graph on outgoing channels only.
Dial candidates combine local source addresses with remote listening endpoints
from the registry and learned vertices with the endpoint flag set. Client socket
vertices are never dial targets.
Addresses within the mesh subnet are filtered. UDP writes use the selected source
address and interface through socket control metadata. Establishing a channel
is independent of graph payload availability and of any reverse channel.

Gossip replaces records whole. Receiving a newer record updates the graph
without sending a reply or forwarding it immediately; the next periodic send
carries it. Records have no age-based expiry: the last record of a node that
never returns stays, but its links into other nodes disappear as their channels
expire. Older versions cannot restore a removed vertex or link.

BFS follows the directed endpoint graph, with stable endpoint ordering for
identical path lengths. The complete path is carried unchanged, including local attachment edges;
movements between endpoints on the same host require no network packet. The
return path is computed independently. There is no separate per-host
endpoint selector. Graph changes and the one-second local observation pass
rebuild routes.

Status exposes incoming endpoint pairs, the live graph, its vertices, every
known record with its version, and routes keyed by destination endpoint. The
graph owner publishes immutable snapshots through the same mailboxes used for
packets.
Channel identity is a directed endpoint pair. Duplicate WS attachments prefer
the smaller origin hash, then the larger initial packet ID, independently for each
direction. There is at most one pending dial per candidate channel. Accepted WS
connections stay private to the transport and supply a writer and a reader to
separate channels. Each channel can stop or be replaced without stopping its sibling.
Only after both channels are closed is the shared WS socket released. The reader
continues draining WS frames when its mesh receive channel is closed, and closing
one channel does not cancel the shared socket context. Socket failures are handled
by the affected reader/writer. Writes use an unbounded mailbox and run independently
of graph updates. Closing a channel discards its pending queue.
Status exposes `channels` with directed `from`, `to`, `transport`, `outgoing` and
attachment `id`, plus the number of pending dials. It exposes no connections.
Crypto keys are shared across a peer's channels and survive local link expiry.
Every incoming channel keeps a 2048-packet window of accepted packet IDs
below the highest one, so a replayed packet is dropped and cannot refresh a
link or reach the TUN twice, while a packet reordered within the window
still arrives. The window lives with the channel: after a link expires, a
replayed packet can open the channel again for one more expiry period.

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
across busy channels, socket address metadata and ciphertext authentication. Separate tests check
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

## Embedded SSH

`sshd: true` (or `mesh run -sshd`) serves SSH on the node's mesh address,
port `sshd_port` (`-sshd-port`, default 22). The listener lives in a
userspace TCP stack (gVisor netstack) fed straight from decrypted mesh
packets: nothing reaches the kernel, so the server is unreachable from any
system interface, needs no system sshd or firewall rule, and works wherever
mesh runs. TCP to that port on the mesh address never enters the TUN; other
traffic is unaffected.

The host key is the node key, so a client can verify it against the ring
(`<mesh ip> ssh-ed25519 <peer pub>` in `known_hosts`). Any ring member's
Ed25519 key logs in; `sshd_authorized_keys` (`-sshd-authorized-keys`) names an
OpenSSH `authorized_keys` file with extra keys. Sessions run as the user
mesh runs as. When mesh runs as root, `user@` selects the account (uid, gid,
groups, home and login shell from the system). Supported: exec, shell with
pty, env, window resize and exit status; no SFTP or port forwarding yet.

## Local control and web

`control` optionally enables a read-only HTTP server. It accepts only literal
loopback IPs (including `::1`) or `localhost`; omitting it disables control.
The Unix status socket and `status -s` have been removed.

- `GET /status`: raw node status, including 64-bit endpoint IDs.
- `GET /topology`: public registry, all live directed graph edges and selected
  routes. Endpoint IDs are decimal strings so browsers preserve every bit.
- `GET /metrics`: Prometheus text exposition. Counters cover packets by inner
  kind and bytes in each direction, packets an incoming channel rejected by
  reason, graph records applied, stale and invalid, data dropped by a relay
  without a channel or on a local hop outside the graph, TUN packets read,
  unrouted and delivered, link up and down events and failed dials. Gauges
  cover graph edges, vertices, addresses, records, links, channels by
  transport and direction, pending dials, routes, messages queued for the
  graph actor, and per peer: reachability, route length, incoming links, record
  age, vertices and links.
- `GET /config?node=mini`: a bootstrap configuration for an ephemeral registry
  entry. Select by its optional `name` or numeric `index`. Static entries
  cannot be exported as ephemeral nodes. The result includes no private key,
  TLS file paths or host binding overrides, and selects the native default TUN.
  It contains the chosen node and peers with static endpoints, at the default
  registry version `1`; the rest is learned from the mesh.

`mesh web` is a separate, unprivileged process. It reads the localhost control
API and serves the embedded Cytoscape.js 3.34.3 interface without a CDN:
Hosts, Endpoint, Matrix and Config tabs. Matrix entries count transport hops
between nodes in the directed graph; local attachment edges cost zero.
Clicking a matrix cell highlights its path. Data refreshes every three seconds;
unchanged topology preserves the viewport and dragged vertex positions.
`/config` opens the configuration tab, and `/api/config?node=client` downloads JSON.
The web listener can bind a LAN or mesh address; control stays on loopback.

For example:

```sh
curl -f 'http://gateway.example:8059/api/config?node=client' -o config.json
sudo mesh run -c config.json -key-file /path/to/private-key
```

Registry entries may include a `name` used for display and configuration
selection. Names do not participate in the transport protocol or authentication.
Only public registry data is exported; `mesh web` never reads the private key.

## macOS

Darwin builds use the kernel's native `utun`, on both arm64 and amd64, without
an extension or third-party driver. Run `mesh run` as root. Omit `tun` for an
automatically assigned interface, or set it to `utunN`. Linux's `mesh0` name
is not valid on Darwin. `/sbin/ifconfig` assigns the node address and MTU, and
`/sbin/route` adds the mesh subnet through that interface. Closing the process
removes the utun interface and its route. UDP, WS/WSS, key files and the local
HTTP API use the same configuration and protocol as Linux.

The Darwin UDP listener reserves the corresponding localhost TCP port to
prevent two mesh processes from sharing it accidentally. Linux keeps its
namespace-local UDP port guard. Native macOS CI checks creation, encrypted
ICMP round trips at two packet sizes, and restart on both architectures.

## Releases

Use **Actions → Release → Run workflow** on `master`, as in `shitty`. Leave
`tag` empty to pick the next numeric release, or supply that next number.
CI runs Linux e2e, browser, race and coverage checks and native Darwin tests,
packages the tested binaries, creates a draft, attests the archives and
publishes the release. Development stays on `master`; releases create tags.

Exported ephemeral configurations include the selected node and peers with static
endpoints, default registry version 1, and no listeners. Add local endpoints only
when that node should accept new incoming connections. The status API exposes
all graph descriptions under `addresses`; client sockets contain their actual
`udp` or `tcp` address and port with `endpoint: false`. Listeners have
`endpoint: true` and retain `udp`, `ws`, or `wss`.
