#!/usr/bin/env python3
"""Validate daemon counters, merge all e2e coverage, and enforce the floor."""

import argparse
from pathlib import Path
import subprocess
import sys


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--output', required=True)
    parser.add_argument('--minimum', type=float, default=98)
    parser.add_argument('dirs', nargs='+')
    args = parser.parse_args()
    directories = []
    for name in args.dirs:
        root = Path(name)
        daemons = sorted(root.glob('daemon-*'))
        # CLI-only cases still have their counters at the root. Every topology
        # must additionally account for each individual mesh run and restart.
        if not daemons:
            sys.exit(f'no daemon coverage directories in {root}')
        for daemon in daemons:
            if not list(daemon.glob('covcounters.*')):
                sys.exit(f'no daemon counters in {daemon}')
        covered = [p for p in [root, *daemons] if list(p.glob('covcounters.*'))]
        directories.extend(str(p) for p in covered)
        print(f'{root.name}: {len(daemons)} mesh daemon runs')
    subprocess.run(['go', 'tool', 'covdata', 'textfmt', '-i=' + ','.join(directories), '-o', args.output], check=True)
    summary = subprocess.run(['go', 'tool', 'cover', f'-func={args.output}'], check=True, capture_output=True, text=True).stdout
    print(summary)
    total = float(summary.strip().splitlines()[-1].split()[-1].rstrip('%'))
    if total < args.minimum:
        sys.exit(f'coverage {total}% is below {args.minimum}%')


if __name__ == '__main__':
    main()
