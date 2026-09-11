# mesh

[![CI](https://github.com/pg83/mesh/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/pg83/mesh/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/pg83/mesh/branch/master/graph/badge.svg)](https://app.codecov.io/gh/pg83/mesh)
[![Go version](https://img.shields.io/github/go-mod/go-version/pg83/mesh)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Private overlay network for a closed set of nodes. Every node knows every
public key; UDP links use keys derived from those static keys and carry
authenticated encrypted packets; a gossip map on each node describes who can reach whom; the
source picks the whole path and relays only follow it; IP rides on top over a
TUN device.

This is the first version: registry, links, TUN, gossip, the routing map,
relaying, and periodic gossip over the full address closure. Link metrics,
retransmits and additional transports come later.

## Usage

```
mesh keygen                 # prints {"pub": ..., "key": ...}
mesh run -c config.json     # runs a node
mesh status -s status.sock  # dumps links as JSON
```

Config is JSON:

```json
{
  "index": 1,
  "key": "<base64 private key>",
  "port": 7000,
  "subnet": "10.77.0.0/24",
  "status": "/run/mesh/status.sock",
  "registry": [
    {"index": 1, "pub": "<x25519>", "sig": "<ed25519>", "intip": "10.77.0.1", "static": ["5.188.103.251:7064"]},
    {"index": 2, "pub": "<x25519>", "sig": "<ed25519>", "intip": "10.77.0.2", "static": []}
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
context is `mesh/5`; older wire formats are incompatible.

Each registered pair derives a shared secret with X25519 and directional
keys with HKDF-SHA256. The context contains `mesh/5`, the sender's public key
and the receiver's public key. There is no handshake or forward secrecy.

| Type | Layout |
|---|---|
| data transport | `3`, sender index (2), packet ID (8), random nonce (24), XChaCha20-Poly1305 ciphertext and tag (16) |
| gossip transport | `4`, the same remaining header and encryption |

The header is authenticated as associated data. Every packet gets a fresh
random nonce, including after a process restart.

Inner data starts with `1`, hop count (1), cursor (1), then the route. Each
hop is a pair of endpoints: source IP (4), source port (2), destination IP
(4), destination port (2). The original IP packet follows. At most 16 UDP
hops are allowed. A relay checks the receiving pair against the route,
advances the cursor, and sends from the exact next source endpoint to the
exact next destination. Local delivery verifies the destination mesh IP.

Inner gossip starts with `2`, an Ed25519 signature (64), and a JSON object
containing the signer's registry index and an `edges` list. Each record has
`from` and `to` endpoints (`ip` integer and `port`), an `id`, an `alive` flag,
and remaining `ttl` in milliseconds. Gossip is split into batches of up to
six records to keep control packets below the physical MTU used by the lab.
The signer may transmit any part of the graph, including records learned
from other members; the signature authenticates the transmitting member's
report. It does not claim that every reported edge touches that member.

One counter starts at Unix nanoseconds on process startup and increments
for every locally generated graph record and outgoing transport packet.
Forwarding a graph record preserves its ID and reduces its remaining TTL.

## Map and routing

The graph is a map of directed endpoint pairs. A vertex is `(IP, port)`;
registry indexes identify encryption keys, not graph vertices. An internal
mesh address is represented as `(meshIP, 0)`. Each host supplies both edges
between that vertex and each of its actual local UDP endpoints. Static
registry addresses are discovery candidates, not evidence of a live edge.

Receiving an authenticated, non-replayed packet observes precisely its UDP
source and destination pair. The destination comes from socket packet
metadata. That incoming edge remains locally alive while packets arrive,
and is withdrawn after five seconds of silence. The reverse edge is
independent. Any accepted data or gossip packet refreshes the observation.

Every second each host sends its known graph through all combinations of
local endpoints and candidate remote endpoints. Candidates come from the
registry, graph endpoint ownership, and authenticated source addresses.
Learned addresses within the mesh subnet are filtered. Sending uses the
selected source address and interface through UDP socket control metadata.
Discovery does not depend on an existing route or a reverse connection.

Gossip merges each directed pair independently. An omitted pair is unchanged;
a newer record replaces an older version of that pair. Newly learned versions
are forwarded promptly. Relaying or repeating the same version never refreshes
its local expiry. Physical edge records live for at most five seconds from receipt. Internal
IP-to-endpoint attachments and withdrawals retain the 40-second metadata
lifetime. Endpoint ownership is retained separately for encryption: forwarding
a source route does not depend on a relay retaining the complete graph.
The highest pair versions remain remembered after expiry to reject stale reintroduction.
Local observations generate fresh versions each second.

BFS follows the directed endpoint graph, with stable endpoint ordering for
identical path lengths. The resulting path is compiled into concrete UDP
hops; movements between endpoints on the same host require no packet. The
return path is computed independently. There is no separate per-host
endpoint selector. Changes and the one-second expiry pass rebuild routes.

Status exposes incoming endpoint pairs, the live graph, its vertices, and
routes keyed by destination endpoint. The runtime keeps one state mutex and
the existing receive, TUN, timer, status, and signal loops. Crypto keys and
the 1024-slot receive window stay shared across a peer's endpoints and survive
link expiry; restarting a receiver resets its replay history.

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
with deterministic loss and delay. UDP probes check packet sizes, replay,
reordering and packets older than the replay window. Separate tests check
gossip on every endpoint during UDP traffic, data keeping a link alive
when gossip is dropped, five-second expiry, replay after expiry, and a
20-second RTT carrying UDP traffic without link flaps. Gossip expiry, unknown
keys, CLI errors and malformed packets also have separate tests. `mesh-probe` is built only with the `meshprobe` tag for the
protocol test; it sends authenticated malformed messages to real mesh nodes.
It is absent from the production binary and its coverage profile.

`./build -j 4 test` runs the suite. `./build -j 4 -Dcoverage coverage` runs it
against the instrumented daemon and enforces 95% statement coverage. Each
individual daemon run has a separate counter directory; shutdown waits for
all processes and missing daemon counters fail the run. CLI coverage is
merged too. Codecov receives that same profile and requires 95% coverage.

The additional twenty protocol and application scenarios are listed in
[tst/SCENARIOS.md](tst/SCENARIOS.md).

On failure the suite prints application/mesh logs and channel counters. Set
`MESH_TEST_ARTIFACTS` to preserve these along with status snapshots outside
the build temporary directory; CI uploads them as failure artifacts.
