import build

from pathlib import Path


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
GO_TEST_SOURCES = build.glob("$(S)/*_test.go")

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

mesh = command(
    name="mesh",
    inputs=GO_INPUTS,
    outputs=["$(B)/bin/mesh"],
    cmd=[
        "go", "build",
        "-trimpath",
        "-buildvcs=false",
        "-o", "$(B)/bin/mesh",
        ".",
    ],
    cwd="$(S)",
    env=GO_ENV,
    descr="GO",
    color="cyan",
)

go_test_stamp = "$(B)/tests/go.stamp"
go_test = command(
    name="go_test",
    inputs=[*GO_INPUTS, *GO_TEST_SOURCES],
    outputs=[go_test_stamp],
    cmd=[
        ["go", "test", "-count=1", "-timeout=2m", "."],
        touch(go_test_stamp),
    ],
    cwd="$(S)",
    env=GO_ENV,
    descr="UT",
    color="green",
)

e2e_tests = []
for test_path in build.glob("$(S)/tst/test_*.py"):
    test_name = test_path.rsplit("/", 1)[-1][len("test_"):-len(".py")]
    test_stamp = f"$(B)/tests/{test_name}.stamp"
    e2e_tests.append(command(
        name=f"e2e_{test_name}",
        inputs=[test_path, "$(S)/tst/lib.py"],
        outputs=[test_stamp],
        deps=[mesh],
        cmd=[
            ["python3", test_path],
            touch(test_stamp),
        ],
        cwd="$(S)",
        env={
            "MESH_TEST_BINARY": mesh.outputs[0],
            "PYTHONDONTWRITEBYTECODE": "1",
        },
        descr="EE",
        color="green",
    ))

group("install", mesh)
group("unit", go_test)
group("e2e", *e2e_tests)
group("test", go_test, *e2e_tests)
