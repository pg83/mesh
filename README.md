# mesh

Private overlay network for a closed set of nodes. Every node knows every
public key; links are UDP sessions negotiated with Noise IK and carried by a
symmetric AEAD; a gossip map on each node describes who can reach whom; the
source picks the whole path and relays only follow it; IP rides on top over a
TUN device.

This is the first version: registry, links, TUN, direct delivery, and the
dial loop over the static address set. Gossip, the map, and relaying through
other nodes come next and do not change the wire format.

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
    {"index": 1, "pub": "<base64>", "intip": "10.77.0.1", "static": ["5.188.103.251:7064"]},
    {"index": 2, "pub": "<base64>", "intip": "10.77.0.2", "static": []}
  ]
}
```

The registry is the same on every node: index, public key, internal address,
and the static endpoints a node has, if any. A node without static endpoints
is never dialed; it dials. `tun` (default `mesh0`) and `mtu` (default 1380)
are optional.

## Wire format

Outer packet, first byte is the type:

| Type | Layout |
|---|---|
| init | `1`, sender id (4), Noise IK message 1 with an 8-byte timestamp payload |
| response | `2`, receiver id (4), sender id (4), Noise IK message 2 |
| transport | `3`, receiver id (4), counter (8), ChaCha20-Poly1305 over the inner packet, header as associated data |

Inner packet, first byte is the type: `0` keepalive, `1` data. Data carries
src index (2), hop count (1), the path as indexes (2 each), the cursor (1),
then the IP packet. A relay checks that the cursor points at itself, advances
it, and hands the packet to the session of the next index.

## Behaviour

- One session per peer, bound to the peer identity. Any authenticated packet
  updates the remote endpoint, so a peer can roam.
- Keepalive after 5 s idle, session dropped after 15 s without traffic.
- Dialing knocks on every known address of every peer without a session, with
  per-address backoff from 1 s to 5 min. Crossed handshakes: the larger index
  gives up its own attempt. An init from a peer that already has a session
  replaces it; a replayed init is rejected by its timestamp.
- Replay protection on transport counters with a 1024-slot window.

## Development

```
./build          # .build/bin/mesh, published as ./mesh
./build test     # go test + e2e topologies in tst/
./lint.sh        # house style gate
```

E2E tests run nodes in separate network namespaces wired by a userspace
switch (`tst/lib.py`); see `CLAUDE.md`.

## License

MIT. See [LICENSE](LICENSE).
