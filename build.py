import build
import os
import zlib

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

# The same daemon, built to be refused by the operating system now and then.
# Only the points a node is meant to survive are armed by default; the ones a
# node is meant to die on are named in the scenarios that expect the death.
chaos_binary = command(
    name="chaos-binary",
    inputs=GO_INPUTS,
    outputs=["$(B)/bin/mesh-chaos"],
    cmd=[
        "go", "build",
        "-trimpath",
        "-tags=meshchaos",
        *(["-cover", "-covermode=atomic"] if COVERAGE else []),
        "-o", "$(B)/bin/mesh-chaos", ".",
    ],
    cwd="$(S)",
    env=GO_ENV,
    descr="GO",
    color="cyan",
)

# How often each point is refused, as one call in so many, and it is every
# so-manyth call rather than a chance. Breaking a websocket connection is not
# here: it is a normal event for that transport, but a scenario that asserts a
# working connection is never replaced cannot survive one, so those points are
# armed in tst/test_refusals_ws.py instead. A scan of the
# interfaces is made of several calls and the whole scan is retried when any of
# them fails, so those two are rare on purpose: a node that can never read its
# own interfaces is not a node under test, it is a node that is broken.
CHAOS_POINTS = ",".join([
    "accept:5",
    "dial pause:5",
    "interface pause:3",
    "implicit socket:20",
    "interface addresses:200",
    "interface event:30",
    "interfaces:200",
    "routes:100",
    "socket read:1000",
    "udp write:2000",
])

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

# The scenarios that put a hand written peer where a node would be.
NEEDS_PROBE = (
    "echo", "gossip_binary", "gossip_collision", "gossip_record", "gossip_stale", "graph_exchange",
    "protocol", "registry_binary", "replay", "route_attachment", "route_payload",
    "ws_channels", "ws_loop_back", "ws_outgoing", "ws_protocol", "ws_proxy", "ws_source",
)

e2e_tests = []
chaos_tests = []
coverage_dirs = []
chaos_coverage_dirs = []
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

    inputs = [test_path, *(["$(S)/tst/browser.py"] if test_name == "web" else []), *(["$(S)/tst/dns.py"] if test_name.startswith("dns") else []), "$(S)/tst/lib.py", "$(S)/tst/work_load.py", "$(S)/tst/program.py", *(["$(S)/tst/nat.py"] if test_name in ("nat", "quic_nat", "nat_punch") else []), *(["$(S)/tst/ws.py"] if "ws" in test_name else [])]
    helpers = [probe] if test_name in NEEDS_PROBE else [quic] if test_name.startswith("quic") else []

    # The same scenario against a daemon the kernel refuses now and then. The
    # seed comes from the name, so a point that breaks breaks again on a rerun.
    chaos_stamp = f"$(B)/chaos/{test_name}.stamp"
    chaos_env = {
        **{k: v for k, v in env.items() if k not in ("GOCOVERDIR", "MESH_TEST_WEB_COVERAGE")},
        "MESH_TEST_BINARY": chaos_binary.outputs[0],
        "MESH_CHAOS": CHAOS_POINTS,
        "MESH_CHAOS_SEED": str(zlib.crc32(test_name.encode()) % 100000),
    }
    chaos_outputs = [chaos_stamp]
    chaos_prelude = []

    # What the refusals walk is exactly what the plain suite cannot reach, so
    # these counters are worth keeping apart and reading on their own.
    if COVERAGE:
        chaos_env["GOCOVERDIR"] = f"$(B)/coverage-chaos/{test_name}"
        chaos_prelude = [mkdir(chaos_env["GOCOVERDIR"])]
        chaos_coverage_dirs.append(chaos_env["GOCOVERDIR"])
        chaos_outputs.append(chaos_env["GOCOVERDIR"])

    chaos_tests.append(command(
        name=f"chaos_{test_name}",
        inputs=inputs,
        outputs=chaos_outputs,
        deps=[chaos_binary, *helpers],
        cmd=[
            *chaos_prelude,
            ["python3", test_path],
            touch(chaos_stamp),
        ],
        cwd="$(S)",
        env=chaos_env,
        descr="KO",
        color="red",
    ))

    e2e_tests.append(command(
        name=f"e2e_{test_name}",
        inputs=[test_path, *(["$(S)/tst/browser.py"] if test_name == "web" else []), *(["$(S)/tst/dns.py"] if test_name.startswith("dns") else []), "$(S)/tst/lib.py", "$(S)/tst/work_load.py", "$(S)/tst/program.py", *(["$(S)/tst/nat.py"] if test_name in ("nat", "quic_nat", "nat_punch") else []), *(["$(S)/tst/ws.py"] if "ws" in test_name else [])],
        outputs=outputs,
        deps=[mesh, probe] if test_name in NEEDS_PROBE else [mesh, quic] if test_name.startswith("quic") else [mesh],
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

# A point that is armed but never consulted refuses nothing and says nothing.
chaos_points = command(
    name="chaos-points",
    inputs=[*GO_SOURCES, "$(S)/dev/chaos_points.py"],
    outputs=["$(B)/chaos-points.stamp"],
    cmd=[
        ["python3", "$(S)/dev/chaos_points.py"],
        touch("$(B)/chaos-points.stamp"),
    ],
    cwd="$(S)",
    descr="KO",
    color="red",
)

group("install", mesh)
group("e2e", *e2e_tests)
group("test", *e2e_tests)
group("chaos", chaos_points, *chaos_tests)

if COVERAGE:
    chaos_coverage = command(
        name="coverage-chaos",
        inputs=["$(S)/dev/coverage.py"],
        outputs=["$(B)/coverage-chaos.out"],
        deps=chaos_tests,
        cmd=["python3", "$(S)/dev/coverage.py", "--output", "$(B)/coverage-chaos.out", "--minimum", "0", *chaos_coverage_dirs],
        cwd="$(S)",
        env=GO_ENV,
        descr="CV",
        color="magenta",
    )

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
