#!/usr/bin/env python3
"""Report the web UI's line coverage from the browser run and enforce the floor."""

import argparse
from pathlib import Path
import sys


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('report')
    parser.add_argument('--minimum', type=float, default=80)
    args = parser.parse_args()
    path = Path(args.report)
    if not path.exists():
        sys.exit(f'{path} is missing: the browser did not run')
    found, missed = 0, []
    for line in path.read_text().splitlines():
        if line.startswith('DA:'):
            number, count = line[3:].split(',')
            found += 1
            if not int(count):
                missed.append(number)
    if not found:
        sys.exit(f'{path} carries no measured line')
    total = round(100 * (found - len(missed)) / found, 1)
    print(f'web coverage {total}% ({found - len(missed)}/{found} lines)')
    if missed:
        print('lines never run: ' + ' '.join(missed))
    if total < args.minimum:
        sys.exit(f'web coverage {total}% is below {args.minimum}%')


if __name__ == '__main__':
    main()
