#!/usr/bin/env python3
# Copyright (C) 2026 Shitty team
# MIT licensed
# Release procedure adapted from pg83/shitty; see LICENSE.

import argparse
import hashlib
import gzip
import json
import os
import re
import shlex
import shutil
import subprocess
import sys
import tarfile
from pathlib import Path


def run(
    arguments: list[str],
    *,
    cwd: Path | None = None,
    capture: bool = False,
) -> str:
    location = f" (in {cwd})" if cwd is not None else ""
    print(f"+ {shlex.join(arguments)}{location}", file=sys.stderr)
    result = subprocess.run(
        arguments,
        cwd=cwd,
        check=True,
        stdout=subprocess.PIPE if capture else None,
        text=True,
    )
    return result.stdout.strip() if capture else ""


def verify_tools() -> None:
    for tool in ("git", "gh", "file"):
        if not shutil.which(tool):
            raise RuntimeError(f"required tool is not available: {tool}")


def release_tags(repository: str) -> list[int]:
    releases = json.loads(run(
        [
            "gh",
            "release",
            "list",
            "--repo",
            repository,
            "--limit",
            "1000",
            "--json",
            "tagName",
        ],
        capture=True,
    ))
    return [
        int(release["tagName"])
        for release in releases
        if re.fullmatch(r"[1-9][0-9]*", release["tagName"])
    ]


def release_exists(repository: str, tag: str) -> bool:
    return subprocess.run(
        ["gh", "release", "view", tag, "--repo", repository],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    ).returncode == 0


def remote_refs(remote: str, tag: str) -> dict[str, str]:
    output = run(
        [
            "git",
            "ls-remote",
            remote,
            f"refs/tags/{tag}",
            f"refs/tags/{tag}^{{}}",
        ],
        capture=True,
    )
    refs = {}
    for line in output.splitlines():
        sha, name = line.split()
        refs[name] = sha
    return refs


def verify_remote_refs(refs: dict[str, str], tag: str, sha: str) -> bool:
    tag_name = f"refs/tags/{tag}"
    tag_commit = refs.get(f"{tag_name}^{{}}", refs.get(tag_name))
    if tag_commit is not None and tag_commit != sha:
        raise RuntimeError(f"remote tag {tag} points to {tag_commit}, not {sha}")
    return tag_name in refs


def tar_info(
    archive: tarfile.TarFile,
    path: Path,
    archive_name: str,
    timestamp: int,
) -> tarfile.TarInfo:
    info = archive.gettarinfo(path, archive_name)
    info.uid = 0
    info.gid = 0
    info.uname = ""
    info.gname = ""
    info.mtime = timestamp
    return info


def write_tar(
    output: Path,
    timestamp: int,
    write_contents,
) -> None:
    with output.open("wb") as compressed_file:
        with gzip.GzipFile(
            filename="",
            mode="wb",
            fileobj=compressed_file,
            compresslevel=9,
            mtime=timestamp,
        ) as gzip_file:
            with tarfile.open(
                fileobj=gzip_file,
                mode="w",
                format=tarfile.GNU_FORMAT,
            ) as archive:
                write_contents(archive)


def create_source_archive(
    checkout: Path,
    output: Path,
    prefix: str,
    timestamp: int,
) -> None:
    tracked = subprocess.check_output(
        ["git", "ls-files", "-z"],
        cwd=checkout,
    ).split(b"\0")
    paths = sorted(
        Path(os.fsdecode(name))
        for name in tracked
        if name
    )

    def write_contents(archive: tarfile.TarFile) -> None:
        root = tarfile.TarInfo(f"{prefix}/")
        root.type = tarfile.DIRTYPE
        root.mode = 0o755
        root.uid = 0
        root.gid = 0
        root.mtime = timestamp
        archive.addfile(root)
        for relative in paths:
            if relative.is_absolute() or ".." in relative.parts:
                raise RuntimeError(f"unsafe tracked path: {relative}")
            source = checkout / relative
            info = tar_info(
                archive,
                source,
                f"{prefix}/{relative.as_posix()}",
                timestamp,
            )
            if info.isreg():
                with source.open("rb") as input_file:
                    archive.addfile(info, input_file)
            else:
                archive.addfile(info)

    write_tar(output, timestamp, write_contents)


def create_binary_archive(
    binary: Path,
    binary_name: str,
    output: Path,
    timestamp: int,
) -> None:
    def write_contents(archive: tarfile.TarFile) -> None:
        info = tar_info(archive, binary, binary_name, timestamp)
        info.mode = 0o755
        with binary.open("rb") as input_file:
            archive.addfile(info, input_file)

    write_tar(output, timestamp, write_contents)


def main() -> int:
    parser = argparse.ArgumentParser(description="Assemble and publish a Mesh GitHub release.")
    parser.add_argument("tag", nargs="?", help="numeric release tag; omitted picks the next")
    parser.add_argument("--sha", default="HEAD", help="git commit to release")
    parser.add_argument("--binaries-directory", type=Path, required=True)
    parser.add_argument("--artifacts-directory", type=Path, required=True)
    parser.add_argument("--generate-notes", action="store_true")
    parser.add_argument("--draft", action="store_true")
    parser.add_argument("--tag-file", type=Path)
    args = parser.parse_args()
    if args.tag is not None and not re.fullmatch(r"[1-9][0-9]*", args.tag):
        parser.error("tag must be a positive decimal integer without leading zeroes")
    verify_tools()
    notes = "" if args.generate_notes else sys.stdin.read().strip()
    if not notes and not args.generate_notes:
        parser.error("release notes must be provided on stdin")

    root = Path(__file__).resolve().parent.parent
    repository = run(["gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner"], cwd=root, capture=True)
    sha = run(["git", "rev-parse", "--verify", f"{args.sha}^{{commit}}"], cwd=root, capture=True)
    if sha != run(["git", "rev-parse", "HEAD"], cwd=root, capture=True):
        raise RuntimeError("check out the release commit before packaging")
    expected_tag = max(release_tags(repository), default=0) + 1
    tag = args.tag or str(expected_tag)
    if release_exists(repository, tag):
        raise RuntimeError(f"release {tag} already exists")
    if int(tag) != expected_tag:
        raise RuntimeError(f"next release tag is {expected_tag}, not {tag}")
    tag_exists = verify_remote_refs(remote_refs("origin", tag), tag, sha)
    timestamp = int(run(["git", "show", "-s", "--format=%ct", sha], cwd=root, capture=True))
    out = args.artifacts_directory.resolve()
    out.mkdir(parents=True, exist_ok=True)
    artifacts = []
    targets = {
        "linux-amd64": ("ELF 64-bit LSB", "x86-64", "executable"),
        "darwin-arm64": ("Mach-O 64-bit", "arm64", "executable"),
        "darwin-amd64": ("Mach-O 64-bit", "x86_64", "executable"),
    }
    for target, markers in targets.items():
        binary = args.binaries_directory.resolve() / target / "mesh"
        description = run(["file", os.fspath(binary)], capture=True)
        if not binary.is_file() or not all(marker in description for marker in markers):
            raise RuntimeError(f"unexpected {target} artifact: {description}")
        archive = out / f"mesh-{target}.tar.gz"
        create_binary_archive(binary, "mesh", archive, timestamp)
        artifacts.append(archive)
    source = out / f"{tag}.tar.gz"
    create_source_archive(root, source, f"mesh-{tag}", timestamp)
    artifacts.append(source)
    checksums = out / "SHA256SUMS"
    checksums.write_text("".join(f"{hashlib.sha256(a.read_bytes()).hexdigest()}  {a.name}\n" for a in artifacts))
    artifacts.append(checksums)
    notes_file = out / "release-notes.md"
    if not args.generate_notes:
        notes_file.write_text(f"{notes}\n")
    if not tag_exists:
        run(["git", "tag", "-a", tag, sha, "-m", f"Release {tag}"], cwd=root)
        run(["git", "push", "origin", f"refs/tags/{tag}:refs/tags/{tag}"], cwd=root)
    run([
        "gh", "release", "create", tag, *(os.fspath(a) for a in artifacts),
        "--repo", repository, "--verify-tag", "--title", tag,
        *(["--generate-notes"] if args.generate_notes else ["--notes-file", os.fspath(notes_file)]),
        *(["--draft"] if args.draft else []),
    ], cwd=root)
    if args.tag_file is not None:
        args.tag_file.write_text(f"{tag}\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
