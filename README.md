# mesh

[![CI](https://github.com/pg83/mesh/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/pg83/mesh/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/pg83/mesh/branch/main/graph/badge.svg)](https://app.codecov.io/gh/pg83/mesh)
[![Go version](https://img.shields.io/github/go-mod/go-version/pg83/mesh)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Private overlay network for a closed set of nodes. Every node knows every
public key; links are UDP sessions negotiated with Noise IK and carried by a
symmetric AEAD; a gossip map on each node describes who can reach whom; the
source picks the whole path and relays only follow it; IP rides on top over a
TUN device.

This is the first version: registry, links, TUN, gossip, the routing map,
relaying, and the dial loop over the full address closure. Link metrics,
retransmits and additional transports come later and do not change the wire
format.

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
address, and the static endpoints a node has, if any. A node without static
endpoints is never dialed by a node that has not heard of it; it dials, and
its own advertised addresses let others dial it back later. `tun` (default
`mesh0`) and `mtu` (default 1380) are optional.

`key` is one 32-byte seed. The Noise static keypair (`pub`) and the
advertisement signing keypair (`sig`) are both derived from it: an
advertisement travels past its author, so the link cipher cannot vouch for
it and it carries its own signature.

## Wire format

Outer packet, first byte is the type:

| Type | Layout |
|---|---|
| init | `1`, sender id (4), Noise IK message 1 with an 8-byte timestamp payload |
| response | `2`, receiver id (4), sender id (4), Noise IK message 2 |
| transport | `3`, receiver id (4), counter (8), ChaCha20-Poly1305 over the inner packet, header as associated data |

Inner packet, first byte is the type: `0` keepalive, `1` data, `2`
advertisement. Data carries src index (2), hop count (1), the path as indexes
(2 each), the cursor (1), then the IP packet. A relay checks that the cursor
points at itself, advances it, and hands the packet to the session of the
next index. An advertisement carries a 64-byte signature and the JSON body.

## Map

Every node floods one advertisement about itself: its index, a timestamp, the
addresses it offers, and the peers it currently has a link with. Timestamps
are per-node counters and are only ever compared with another advertisement
of the same node; expiry runs on local arrival time instead. A newer
advertisement is stored and passed on to every link except the one it came
from, so it stops spreading on its own. A link coming up hands the new peer
the whole database at once.

Routes are a breadth-first search by hop count over the advertised graph,
recomputed whenever it changes. The source puts the whole path into the
packet, so relays make no decisions and loops cannot form.

Dialing knocks on the closure of known addresses: the statics from the
registry plus everything a peer advertises, which arrives through the mesh.
So a node reachable only through a relay today becomes directly reachable as
soon as one of its addresses works. Addresses inside the mesh subnet are
never dialed: the overlay must not run over itself.

## Behaviour

- One session per peer, bound to the peer identity. Any authenticated packet
  updates the remote endpoint, so a peer can roam.
- Keepalive after 5 s idle, session dropped after 15 s without traffic.
- Advertisement every 10 s and on every link change, expired after 40 s.
- Dialing knocks on every known address of every peer without a session, with
  per-address backoff from 1 s to 5 min. Crossed handshakes: the larger index
  gives up its own attempt. An init from a peer that already has a session
  replaces it; a replayed init is rejected by its timestamp.
- Replay protection on transport counters with a 1024-slot window.

## Development

```
./build          # .build/bin/mesh, published as ./mesh
./build test     # go test + e2e topologies in tst/
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
seconds, allowing for the current 15-second session timeout and TCP retries.

Other scenarios transfer and hash files through scp and curl while cutting
the active path, synchronize trees with rsync, and run iperf3 TCP/UDP streams
with deterministic loss and delay. UDP probes check packet sizes, replay,
reordering and packets older than the replay window. Gossip expiry,
handshake loss/replay, unknown keys, CLI errors and malformed packets have
separate tests. `mesh-probe` is built only with the `meshprobe` tag for the
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
