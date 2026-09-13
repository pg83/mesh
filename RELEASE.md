Mesh now exposes read-only HTTP control on localhost instead of a Unix status socket. A separate `mesh web` process serves the topology, a directed hop matrix and complete ephemeral-node configurations without private keys.

- `control: "127.0.0.1:8058"` enables the API.
- `mesh web -control 127.0.0.1:8058 -listen 127.0.0.1:8059` opens the Hosts / Endpoint / Matrix / Config interface.
- Download `/api/config?node=mini`, then run `sudo mesh run -c mesh.json -key-file ~/.ssh/mini.key`.
- Native Darwin TUN support on Apple Silicon and Intel. Leave `tun` unset for automatic `utun` allocation; Linux defaults to `mesh0`.

The transport remains compatible with release 6. Configurations using `status` must switch to `control`; `mesh status` now accepts `-control` instead of `-s`.

Release publication follows Linux e2e, Chromium UI, race detector, coverage, and native macOS TUN round-trip checks. Darwin archives contain the binaries exercised by the native CI jobs. Extract the archive for your architecture; each contains the executable `mesh`. `SHA256SUMS` covers all three archives.
