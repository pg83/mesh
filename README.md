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

`key` is one 32-byte seed. The X25519 static keypair (`pub`) and the
advertisement signing keypair (`sig`) are both derived from it: an
advertisement travels past its author, so the link cipher cannot vouch for
it and it carries its own signature.

## Wire format

All multibyte integers in the mesh protocol use little-endian order.
Encapsulated IP packets retain their standard network format. The key
context is `mesh/3`; older wire formats are incompatible, so upgrade all
nodes together.

Each pair derives a shared secret with X25519 and directional encryption
keys with HKDF-SHA256. The HKDF context contains `mesh/3`, the sender's public
key and the receiver's public key, in that order. There is no handshake and
no forward secrecy.

The only outer packet type is transport:

| Type | Layout |
|---|---|
| transport | `3`, sender index (2), packet ID (8), random nonce (24), XChaCha20-Poly1305 ciphertext and tag (16) |

The entire header is authenticated as associated data. A fresh random nonce
for every packet avoids encryption nonce reuse under the static key across
process restarts.

Inner packet, first byte is the type: `1` data, `2` advertisement. Data carries src index (2), hop count (1), the path as indexes
(2 each), the cursor (1), then the IP packet. A relay checks that the cursor
points at itself, advances it, and hands the packet to the session of the
next index. An advertisement carries a 64-byte signature and the JSON body.

One packet counter is initialized from Unix nanoseconds when the node starts.
Every outgoing transport packet and every locally authored advertisement
increments that same counter, across all peers and endpoints. Forwarded
advertisements retain their author's ID inside a fresh transport packet.

## Map

Every node floods one advertisement about itself: its index, a packet ID in
the `ts` field, the addresses it offers, and the peers it currently has a link
with. Advertisement IDs are only compared with another advertisement of the
same node; expiry runs on local arrival time instead. A newer
advertisement is stored and passed on to peers with an observed endpoint,
except the one it came from, so it stops spreading on its own. This includes
a peer whose incoming traffic has timed out: the outgoing direction can
still deliver the update about the lost link. A link coming up hands the new peer
the whole database at once.

Routes are a breadth-first search by hop count over the advertised graph,
recomputed whenever it changes. An edge is usable only while both nodes
advertise each other; receiving packets alone does not prove that the
opposite direction works. The source puts the whole path into the
packet, so relays make no decisions and loops cannot form.

Every second, a node sends its signed advertisement to every known endpoint of every
peer: static registry addresses, advertised addresses learned through gossip,
and the last authenticated source address. A peer reachable only through a
relay can become directly reachable as soon as an endpoint works. Learned
addresses inside the mesh subnet are filtered; static registry addresses
are used as configured.

## Behaviour

- No handshake, separate keepalive or exponential backoff. Own gossip goes
  to every endpoint once per second, regardless of data traffic or link state.
- Any authenticated, non-replayed packet refreshes the peer's activity and
  updates its remote endpoint, so a peer can roam.
- A link becomes alive on the first accepted packet and expires after five
  seconds without accepted packets, checked by the one-second timer.
- Advertisement every second and on every link change, expired after 40 s.
- Replay protection on packet IDs with a 1024-slot window. Derived keys and
  replay state survive link expiry within the running process; a receiver
  restart resets its replay history.

## Development

```
./build          # .build/bin/mesh, published as ./mesh
./build test     # e2e topologies in tst/
./lint.sh        # house style gate, needs a sibling ay checkout
```

CI runs the same `./build test` on every push and pull request. The lab needs
unprivileged user namespaces and the tun module, which the workflow enables.

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
The stress test runs in both CI jobs as part of the regular e2e suite.

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

On failure the suite prints application/mesh logs and channel counters. Set
`MESH_TEST_ARTIFACTS` to preserve these along with status snapshots outside
the build temporary directory; CI uploads them as failure artifacts.
