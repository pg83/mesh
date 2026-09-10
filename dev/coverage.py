#!/usr/bin/env python3
"""Merges the coverage counters the instrumented mesh wrote during the tests
(one GOCOVERDIR per test node) into a text profile and prints the total."""

import argparse
import glob
import os
import subprocess
import sys


def counters(directory):
    return glob.glob(os.path.join(directory, "covcounters.*"))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", required=True)
    parser.add_argument("dirs", nargs="+")
    args = parser.parse_args()

    empty = [d for d in args.dirs if not counters(d)]
    if empty:
        # every test node runs the binary many times, and the binary writes
        # its counters at exit, so an empty directory means the data went
        # missing rather than that nothing was measured
        sys.exit("no coverage counters in:\n  " + "\n  ".join(empty))

    for directory in args.dirs:
        print(f"{len(counters(directory)):5d} processes  {os.path.basename(directory)}")

    subprocess.run(
        ["go", "tool", "covdata", "textfmt", "-i=" + ",".join(args.dirs), "-o", args.output],
        check=True,
    )
    summary = subprocess.run(
        ["go", "tool", "cover", f"-func={args.output}"],
        check=True, capture_output=True, text=True,
    ).stdout
    print(summary.strip().splitlines()[-1])


if __name__ == "__main__":
    main()
