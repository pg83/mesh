import build
import os

from pathlib import Path


build.flags.allow({
    "coverage": {
        "descr": "instrument the binary; `./build -Dcoverage coverage` writes $(B)/coverage.out",
        "default": "",
    },
})

COVERAGE = bool(build.flags.coverage)


def coverage_dir(name):
    return f"$(B)/coverage/{name}"


def mkdir(path):
    return [
        "python3",
        "-c",
        f"from pathlib import Path; Path(r'{path}').mkdir(parents=True, exist_ok=True)",
    ]


ROOT = Path(__file__).parent


def source_files(directory):
    root = ROOT / directory
    return [
        "$(S)/" + path.relative_to(ROOT).as_posix()
        for path in sorted(root.rglob("*"))
        if path.is_file()
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
    "$(S)/go.mod",
    "$(S)/go.sum",
    *source_files("vendor"),
]

GO_ENV = {
    "CGO_ENABLED": "0",
    "GOFLAGS": "-mod=vendor -buildvcs=false",
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

e2e_tests = []
coverage_dirs = []
for test_path in build.glob("$(S)/tst/test_*.py"):
    test_name = test_path.rsplit("/", 1)[-1][len("test_"):-len(".py")]
    test_stamp = f"$(B)/tests/{test_name}.stamp"
    env = {
        "MESH_TEST_ARTIFACTS": os.environ.get("MESH_TEST_ARTIFACTS", ""),
        "MESH_TEST_BINARY": mesh.outputs[0],
        "MESH_TEST_PROBE": probe.outputs[0],
        "PYTHONDONTWRITEBYTECODE": "1",
    }
    prelude = []
    outputs = [test_stamp]

    if COVERAGE:
        # the counters are a declared output so the coverage node sees them
        env["GOCOVERDIR"] = coverage_dir(test_name)
        prelude = [mkdir(env["GOCOVERDIR"])]
        coverage_dirs.append(env["GOCOVERDIR"])
        outputs.append(env["GOCOVERDIR"])

    e2e_tests.append(command(
        name=f"e2e_{test_name}",
        inputs=[test_path, "$(S)/tst/lib.py", "$(S)/tst/workload.py", "$(S)/tst/program.py"],
        outputs=outputs,
        deps=[mesh, probe] if test_name == "protocol" else [mesh],
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
