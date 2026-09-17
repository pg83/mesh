#!/usr/bin/env python3
"""Merge the Go coverage profiles of several runs into one and say what each added."""

import argparse
from pathlib import Path
import sys


def read(path, skip):
    """A profile as {block: (statements, count)}. Identical blocks are counted
    once at the highest count seen, which is what running the same code twice
    means."""
    blocks = {}
    for line in Path(path).read_text().splitlines():
        if not line or line.startswith('mode:'):
            continue
        where, statements, count = line.rsplit(' ', 2)

        if any(where.split(':')[0].endswith(name) for name in skip):
            continue

        held = blocks.get(where, (0, 0))
        blocks[where] = (int(statements), max(int(count), held[1]))
    return blocks


def share(blocks):
    total = sum(statements for statements, _ in blocks.values())
    covered = sum(statements for statements, count in blocks.values() if count)
    return covered, total, round(100 * covered / total, 1) if total else 0.0


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('profiles', nargs='+')
    parser.add_argument('--output', required=True)
    parser.add_argument('--minimum', type=float, default=0)
    # A build that carries its own scaffolding, like the one that refuses
    # system calls, measures files the others do not have. They are not the
    # program and they have no place in what the program's coverage is.
    parser.add_argument('--skip', action='append', default=[])
    args = parser.parse_args()
    merged = {}
    for path in args.profiles:
        blocks = read(path, args.skip)

        if not blocks:
            sys.exit(f'{path} carries no measured block')

        # Every profile has a line for every block of the binary, run or not,
        # so two profiles of the same source have the same blocks. Different
        # blocks mean different sources, and adding those up would invent a
        # number that describes neither.
        if merged and set(blocks) != set(merged):
            missing, extra = len(set(merged) - set(blocks)), len(set(blocks) - set(merged))
            sys.exit(f'{path} was measured on other sources: {missing} blocks missing, {extra} unknown')

        covered, total, percent = share(blocks)
        print(f'{Path(path).name}: {percent}% ({covered}/{total} statements)')

        for where, (statements, count) in blocks.items():
            held = merged.get(where, (0, 0))
            merged[where] = (statements, max(count, held[1]))
    covered, total, percent = share(merged)
    Path(args.output).write_text(
        'mode: atomic\n' + ''.join(f'{where} {statements} {count}\n'
                                   for where, (statements, count) in sorted(merged.items())))
    print(f'together: {percent}% ({covered}/{total} statements)')

    if percent < args.minimum:
        sys.exit(f'coverage {percent}% is below {args.minimum}%')


if __name__ == '__main__':
    main()
