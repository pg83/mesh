# mesh

Private overlay network: closed set of nodes with known keys, UDP links,
gossip map on every node, source routing through any node, IP over TUN.

## Conventions

- Style: `STYLE.md`. One `package main`, all `.go` files in the repo root.
- Git author: `claude <claude@users.noreply.github.com>`. Commit messages in English.
- Config is JSON only.

## Build and test

- `./build` builds `.build/bin/mesh` and publishes `./mesh`.
- `./build test` runs the e2e suite in `tst/`. Tests are e2e only; do not add
  Go unit tests.
- Every e2e test is a topology: nodes in separate network namespaces, wired by
  the userspace switch in `tst/lib.py` (this kernel has no veth; TUN only).
  Tests need unprivileged user namespaces; they re-exec under `unshare -rUn`.
- Dependencies are vendored. To update: `GOSUMDB=off go get ... && go mod vendor`
  (the toolchain here ships with an empty `GOSUMDB`).
- `./build -Dcoverage coverage` writes `.build/coverage.out` from the e2e
  suite: the binary is instrumented and each daemon run gets its own
  `GOCOVERDIR`. A mesh node exits cleanly on SIGTERM so those counters
  actually reach disk; killing it with SIGKILL loses them.
- `./lint.sh` before committing style-sensitive changes.

- Application tests require ssh/sshd/ssh-keygen/scp, rsync, curl and iperf3
  (CI installs these explicitly). Python 3.12+ is needed for os.setns.
- Test network faults belong in tst/lib.py's switch. No daemon control API.
- probe.go is a test-only authenticated peer behind the meshprobe build tag;
  main.go is excluded only for that helper binary.
- Keep the same SSH/TCP process alive across failover assertions. Reconnecting
  a client is not a successful migration test.
- Coverage requires counters for every daemon run and at least 95% overall.
