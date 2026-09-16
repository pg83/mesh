import build
import os

build.flags.allow({
    "coverage": {
        "descr": "instrument the binary; `./build -Dcoverage coverage` writes $(B)/coverage.out",
        "default": "",
    },
    "race": {
        "descr": "build mesh with the Go race detector; run with `./build -Drace test`",
        "default": "",
    },
})

COVERAGE = bool(build.flags.coverage)
RACE = bool(build.flags.race)


def coverage_dir(name):
    return f"$(B)/coverage/{name}"


def mkdir(path):
    return [
        "python3",
        "-c",
        f"from pathlib import Path; Path(r'{path}').mkdir(parents=True, exist_ok=True)",
    ]


def touch(path):
    return [
        "python3",
        "-c",
        f"from pathlib import Path; p=Path(r'{path}'); p.parent.mkdir(parents=True, exist_ok=True); p.touch()",
    ]


GO_SOURCES = [
    path for path in build.glob("$(S)/*.go")
    if not path.endswith("_test.go")
]
GO_INPUTS = [
    *GO_SOURCES,
    *build.glob("$(S)/web/*"),
    "$(S)/go.mod",
    "$(S)/go.sum",
]

GO_ENV = {
    "CGO_ENABLED": "1" if RACE else "0",
    "GOFLAGS": "-mod=readonly -buildvcs=false",
    "GOTOOLCHAIN": "local",
    "GOWORK": "off",
}

# With -Dcoverage the binary counts what it executes (Go's own
# instrumentation) and every process writes its counters to GOCOVERDIR at
# exit, so the end-to-end tests measure coverage of the real program.
mesh = command(
    name="mesh",
    inputs=GO_INPUTS,
    outputs=["$(B)/bin/mesh"],
    cmd=[
        "go", "build",
        "-trimpath",
        "-buildvcs=false",
        *(["-race"] if RACE else []),
        *(["-cover", "-covermode=atomic"] if COVERAGE else []),
        "-o", "$(B)/bin/mesh",
        ".",
    ],
    cwd="$(S)",
    env=GO_ENV,
    descr="GO",
    color="cyan",
)

probe = command(
    name="probe",
    inputs=GO_INPUTS,
    outputs=["$(B)/bin/mesh-probe"],
    cmd=["go", "build", "-trimpath", "-tags=meshprobe", "-o", "$(B)/bin/mesh-probe", "."],
    cwd="$(S)",
    env=GO_ENV,
    descr="GO",
    color="cyan",
)

quic = command(
    name="quic",
    inputs=GO_INPUTS,
    outputs=["$(B)/bin/mesh-quic"],
    cmd=["go", "build", "-trimpath", "-tags=meshquic", "-o", "$(B)/bin/mesh-quic", "."],
    cwd="$(S)",
    env=GO_ENV,
    descr="GO",
    color="cyan",
)

e2e_tests = []
coverage_dirs = []
for test_path in build.glob("$(S)/tst/test_*.py"):
    test_name = test_path.rsplit("/", 1)[-1][len("test_"):-len(".py")]
    test_stamp = f"$(B)/tests/{test_name}.stamp"
    env = {
        "MESH_TEST_ARTIFACTS": os.environ.get("MESH_TEST_ARTIFACTS", ""),
        "MESH_TEST_BROWSER_PYTHON": os.environ.get("MESH_TEST_BROWSER_PYTHON", ""),
        "MESH_TEST_BINARY": mesh.outputs[0],
        "MESH_TEST_PROBE": probe.outputs[0],
        "MESH_TEST_QUIC": quic.outputs[0],
        "PYTHONDONTWRITEBYTECODE": "1",
    }
    prelude = []
    outputs = [test_stamp]

    if RACE:
        env["GORACE"] = "halt_on_error=1 atexit_sleep_ms=0"

    if COVERAGE:
        # the counters are a declared output so the coverage node sees them
        env["GOCOVERDIR"] = coverage_dir(test_name)
        prelude = [mkdir(env["GOCOVERDIR"])]
        coverage_dirs.append(env["GOCOVERDIR"])
        outputs.append(env["GOCOVERDIR"])

    # The browser records which lines of the web UI it ran; the scenario writes
    # the record whether or not a browser was there to fill it. It measures the
    # page, not the daemon, so it does not wait for an instrumented build.
    if test_name == "web":
        env["MESH_TEST_WEB_COVERAGE"] = "$(B)/coverage-web.info"
        outputs.append(env["MESH_TEST_WEB_COVERAGE"])

    e2e_tests.append(command(
        name=f"e2e_{test_name}",
        inputs=[test_path, *(["$(S)/tst/browser.py"] if test_name == "web" else []), *(["$(S)/tst/dns.py"] if test_name.startswith("dns") else []), "$(S)/tst/lib.py", "$(S)/tst/work_load.py", "$(S)/tst/program.py", *(["$(S)/tst/nat.py"] if test_name in ("nat", "quic_nat", "nat_punch") else []), *(["$(S)/tst/ws.py"] if "ws" in test_name else [])],
        outputs=outputs,
        deps=[mesh, probe] if test_name in ("echo", "protocol", "gossip_binary", "gossip_record", "replay", "registry_binary", "gossip_stale", "graph_exchange", "route_attachment", "ws_proxy", "ws_protocol", "ws_outgoing", "ws_loop_back", "ws_channels", "ws_source", "route_payload") else [mesh, quic] if test_name.startswith("quic") else [mesh],
        cmd=[
            *prelude,
            ["python3", test_path],
            touch(test_stamp),
        ],
        cwd="$(S)",
        env=env,
        descr="EE",
        color="green",
    ))

group("install", mesh)
group("e2e", *e2e_tests)
group("test", *e2e_tests)

if COVERAGE:
    coverage = command(
        name="coverage",
        inputs=["$(S)/dev/coverage.py"],
        outputs=["$(B)/coverage.out"],
        deps=e2e_tests,
        cmd=["python3", "$(S)/dev/coverage.py", "--output", "$(B)/coverage.out", *coverage_dirs],
        cwd="$(S)",
        env=GO_ENV,
        descr="CV",
        color="magenta",
    )
