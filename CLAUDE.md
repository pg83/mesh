# mesh

Private overlay network: closed set of nodes with known keys, UDP links,
gossip map on every node, source routing through any node, IP over TUN.

## Conventions

- Style: `STYLE.md`. One `package main`, all `.go` files in the repo root.
- Git author: `claude <claude@users.noreply.github.com>`. Commit messages in English.
- Config is JSON only.

## Build and test

- `./build` builds `.build/bin/mesh` and publishes `./mesh`.
- `./build test` runs Go unit tests and the e2e suite in `tst/`.
- Every e2e test is a topology: nodes in separate network namespaces, wired by
  the userspace switch in `tst/lib.py` (this kernel has no veth; TUN only).
  Tests need unprivileged user namespaces; they re-exec under `unshare -rUn`.
- Dependencies are vendored. To update: `GOSUMDB=off go get ... && go mod vendor`
  (the toolchain here ships with an empty `GOSUMDB`).
- `./lint.sh` before committing style-sensitive changes.
