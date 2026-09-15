# mesh

[![CI](https://github.com/pg83/mesh/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/pg83/mesh/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/pg83/mesh/branch/master/graph/badge.svg)](https://app.codecov.io/gh/pg83/mesh/tree/master)
[![Go version](https://img.shields.io/github/go-mod/go-version/pg83/mesh)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Private overlay network for a closed set of nodes. Every node has a key and
a fixed address in the mesh subnet; nodes find each other through a shared
registry, connect over UDP or WebSocket (plain or TLS), encrypt every packet
with keys derived from the pair's public keys, and route IP traffic between
their TUN devices, relaying through other nodes when there is no direct
path. Nodes behind NAT reach each other through any common peer; no STUN or
coordination server is involved. There is no handshake: the first packet
already carries data.

## Usage

```
mesh keygen                 # prints {"pub": ..., "key": ...}
mesh run -c config.json     # runs a node (root, for the TUN device)
mesh run -c config.json -key-file /path/to/private-key
mesh status -control 127.0.0.1:8058
mesh web -control 127.0.0.1:8058 -listen 127.0.0.1:8059
mesh dns -control 127.0.0.1:8058 -listen 127.0.0.1:5355
```

### Configuration

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
    {"index": 1, "name": "gateway", "pub": "<x25519>", "intip": "10.77.0.1", "endpoint": [{"proto": "udp", "addr": "203.0.113.10", "port": 17001, "bind_addr": "192.168.1.20", "bind_port": 7001}]},
    {"index": 2, "name": "laptop", "pub": "<x25519>", "intip": "10.77.0.2", "endpoint": []}
  ]
}
```

| Field | Meaning |
|---|---|
| `index` | This node's registry index, 1–255 |
| `key` | Base64 32-byte seed of the node's X25519 key; `-key-file` overrides it |
| `endpoint` | Listeners this node opens, see below; an empty list is valid |
| `subnet` | The mesh subnet; every node's `intip` lies in it |
| `control` | Optional read-only HTTP API on a loopback address |
| `registry` | Known nodes: `index`, `pub`, `intip`, static `endpoint` list, optional `name` |
| `registry_version` | Version of the records loaded from this file, default `1`; raise it when changing the registry |
| `no_dial` | Optional list of `{"from": ip, "to": ip}` pairs this node never dials |
| `tun`, `mtu` | TUN name (`mesh0` on Linux, `utun` on macOS) and MTU (default 1380); see below for running without a device |
| `sshd`, `sshd_port`, `sshd_authorized_keys` | Embedded SSH server, see below |
| `dns`, `dns_port` | Embedded DNS server for the `mesh` zone, see below |

`-key-file` names a file with either the base64 seed or an unencrypted
OpenSSH Ed25519 private key; for an SSH identity put the full
`ssh-ed25519 AAAA...` line in the registry's `pub`.

Only bootstrap hosts need the complete registry; other nodes can start with
their own entry and those hosts and learn the rest from the mesh. Nodes
introduce each other; a registry record changes only for a strictly higher
version, there is no removal. A node without static endpoints is never
dialed first: it dials, and its own addresses let others dial it back.

### With and without a TUN device

The device is created when the config names one or `mesh run -tun` asks
for it, never with `-no-tun`, and by default unless `-sshd` was given: a
node started for SSH access alone leaves the system's network as it is. A
node without a device still relays traffic between peers and answers SSH
and DNS on its mesh address for them; nothing reaches or leaves its own
system through the mesh.

### Endpoints

Both the local `endpoint` list and registry entries use the same objects.
Endpoints describe listeners; outgoing connections need none.

| Field | Meaning |
|---|---|
| `proto` | `udp`, `ws` or `wss` |
| `addr`, `port` | Address and port advertised to peers |
| `bind_addr`, `bind_port`, `bind_proto` | Local binding when it differs from the advertised one |
| `path` | WebSocket request path including query, default `/` |
| `tls_cert`, `tls_key` | Native TLS for a local `wss` listener |
| `tls_ca` | Private CA bundle a registry `wss` endpoint is checked against |

For UDP, `addr: "0.0.0.0"` listens on every eligible IPv4 interface address
and `"::"` on global or ULA IPv6 addresses; include both for dual stack.
Listeners follow interface changes without a restart. An interface address
without a configured UDP listener gets an implicit socket on a random port,
so a node with an empty `endpoint` list still accepts return traffic from
peers that have seen it.

A port-forwarded node advertises the public pair and binds the private one:
`addr`/`port` are what peers dial, `bind_addr`/`bind_port` where the
forwarded packets arrive. Configure the forwarding on the router; mesh does
not. Nodes behind NAT without forwarding learn each other's mappings from a
common peer and connect directly where the NAT allows it.

For TLS termination at a reverse proxy, advertise the public WSS endpoint
and bind plain WS locally:

```json
{"proto": "wss", "addr": "mesh.example.net", "port": 443, "path": "/mesh",
 "bind_proto": "ws", "bind_addr": "127.0.0.1", "bind_port": 8080}
```

The proxy must keep the Host header and path and support WebSocket
upgrades. Packets are encrypted and authenticated end to end regardless of
TLS.

A node dials a target only from an interface whose network contains it or
through which the system routes it. Failed WebSocket connection attempts
back off exponentially up to a minute; a new interface address, a link from
the peer or a new record of it retries at once. Routes prefer UDP links over
WebSocket ones and shorter paths over longer.

On Linux the TUN interface persists across restarts; remove it with
`ip link del <name>` when renaming or uninstalling. On macOS the interface
disappears with the process.

### Embedded SSH

`sshd: true` (or `mesh run -sshd`) serves SSH on the node's mesh address,
port `sshd_port` (`-sshd-port`, default 22), inside a userspace TCP stack
fed straight from decrypted mesh packets: the server is unreachable from any
system interface and needs no system sshd or firewall rule. The host key is
the node key, so `known_hosts` can hold `<mesh ip> ssh-ed25519 <peer pub>`.
Any ring member's Ed25519 key logs in; `sshd_authorized_keys`
(`-sshd-authorized-keys`) adds an OpenSSH `authorized_keys` file. Sessions
run as the user mesh runs as; when that is root, `user@` selects the
account. Supported: exec, shell with pty, env, window resize and exit
status; no SFTP or port forwarding.

### Names

Every registry entry with a `name` is `<name>.mesh`, and its mesh address
resolves back to that name. `dns: true` (or `mesh run -dns`) makes the node
answer the zone, port `dns_port` (`-dns-port`, default 53), from its own
registry: on its mesh address for peers, and on the subnet's service
address, the first address of the subnet (`10.77.0.0` for `10.77.0.0/24`),
for its own system, whose packets to the node's own address never enter the
TUN. Names are `A` and `PTR` records with a 60-second TTL; other record
types of known names get an empty answer, unknown names in the zone
`NXDOMAIN`, everything outside the zone `REFUSED`, so a resolver must
forward only the zone:

```
resolvectl dns mesh0 10.77.0.0 && resolvectl domain mesh0 '~mesh'   # systemd-resolved
server=/mesh/10.77.0.0                                             # dnsmasq
-u '[/mesh/]10.77.0.0'                                              # dnsproxy
echo 'nameserver 10.77.0.0' > /etc/resolver/mesh                    # macOS
```

`mesh dns` serves the same zone from a node's control API on a plain
socket (`-listen`, default `127.0.0.1:5355`) for resolvers that forward to
localhost; it needs no privileges and no TUN.

### Local control and web

`control` enables a read-only HTTP API on a loopback address:

- `GET /status`: node status: registry, channels, links, graph, records,
  version vectors and routes.
- `GET /topology`: registry, live graph edges and routes for the web page.
- `GET /metrics`: Prometheus text: packets and bytes by kind and direction,
  rejected packets by reason, records and vectors applied, stale and
  invalid, TUN and relay counters, link and dial events, and per-peer
  reachability, route length, links and record age.
- `GET /config?node=laptop`: a bootstrap configuration for a node without
  static endpoints, selected by `name` or `index`: the node, the peers with
  static endpoints, no private key.

`mesh web` is a separate unprivileged process serving a page over the control
API: host and endpoint graphs, a spring simulation of the graph, a hop
matrix with path highlighting, and configuration downloads. It can bind a
LAN or mesh address while control stays on loopback:

```sh
curl -f 'http://gateway.example:8059/api/config?node=laptop' -o config.json
sudo mesh run -c config.json -key-file /path/to/private-key
```

### macOS

Darwin builds use the kernel's `utun` on arm64 and amd64 without any
extension. Run `mesh run` as root; omit `tun` for an automatic interface or
set `utunN`. Configuration and protocol are the same as on Linux.

## Building

```
./build                       # .build/bin/mesh
./build test                  # end-to-end topologies in tst/
./build -Drace test           # the same under the race detector (needs cgo)
./build -Dcoverage coverage   # instrumented run, profile in .build/coverage.out
./lint.sh                     # house style gate, needs a sibling ay checkout
```

Go 1.26 or newer. The end-to-end tests run nodes in network namespaces wired
by a userspace switch and need unprivileged user namespaces, the tun module,
and for some scenarios sshd, iperf3, rsync and a Playwright Chromium; CI runs
them all on every push, plus native macOS checks.

## Releases

**Actions → Release → Run workflow** on `master`, or locally
`python3 dev/release.py <tag> --binaries-directory <dir> --artifacts-directory <out> < notes.md`.
Releases are numbered tags with linux-amd64, darwin-amd64 and darwin-arm64
binaries, a source archive and checksums.

## License

MIT. See [LICENSE](LICENSE).
