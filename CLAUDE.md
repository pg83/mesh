# mesh

Private overlay network: closed set of nodes with known keys, UDP and WS/WSS links,
gossip map on every node, source routing through any node, IP over TUN.

## Conventions

- Style: `STYLE.md`. One `package main`, all `.go` files in the repo root.
- Git author: `claude <claude@users.noreply.github.com>`. Commit messages in English.
- Config is JSON only.

## Build and test

- `./build` builds `.build/bin/mesh` and publishes `./mesh`.
- `./build test` runs the e2e suite in `tst/`. Tests are e2e only; do not add
  Go unit tests.
- `./build -Drace test` runs the same suite with mesh built using `-race`.
  Requires a C compiler; a detected race immediately fails the process.
- Every e2e test is a topology: nodes in separate network namespaces, wired by
  the userspace switch in `tst/lib.py` (this kernel has no veth; TUN only).
  Tests need unprivileged user namespaces; they re-exec under `unshare -rUn`.
- Dependencies are pinned in `go.mod` and `go.sum`; builds use Go modules
  without a vendor directory.
- `./build -Dcoverage coverage` writes `.build/coverage.out` from the e2e
  suite: the binary is instrumented and each daemon run gets its own
  `GOCOVERDIR`. A mesh node exits cleanly on SIGTERM so those counters
  actually reach disk; killing it with SIGKILL loses them.
- `./build chaos` runs every scenario again against `mesh-chaos`, the same
  daemon built behind the `meshchaos` tag. Its `Syscalls` implementation
  refuses some calls the way the kernel is entitled to. `MESH_CHAOS` names the
  points and how often each fails, `MESH_CHAOS_SEED` makes the choice
  repeatable, and only `mesh run` is ever armed. Every call into the operating
  system that can fail belongs in `syscalls.go`; network faults still belong to
  the switch in `tst/lib.py`.
- Coverage is reported from every run together: the plain suite and the chaos
  suite each hand their profile to the aggregate job, which merges them with
  `dev/merge_coverage.py` and uploads one report. Profiles measured on
  different sources are refused rather than added up.
- `./lint.sh` before committing style-sensitive changes.

- Application tests require ssh/sshd/ssh-keygen/scp, rsync, curl, iperf3 and openssl
  (CI installs these explicitly). Python 3.12+ is needed for os.setns.
- Test network faults belong in tst/lib.py's switch. No daemon control API.
- probe.go is a test-only authenticated peer behind the meshprobe build tag;
  main.go is excluded only for that helper binary.
- quic.go is a test-only QUIC server/client behind the meshquic build tag.
  tst/test_quic.py runs four clients for 30 seconds in every CI e2e run.
- Keep the same SSH/TCP process alive across failover assertions. Reconnecting
  a client is not a successful migration test.
- Coverage requires counters for every daemon run. One suite has to clear 95%;
  the 98% floor is on the runs added up, in the aggregate job.
- The browser records which lines of `web/app.js` it ran into
  `.build/coverage-web.info`; `dev/web_coverage.py` holds that floor at 80%.
