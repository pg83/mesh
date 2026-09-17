#!/usr/bin/env python3
"""Every point the chaos build can refuse has to be asked about on every platform.

A point that is declared and armed but never consulted refuses nothing, and
nothing says so: the run comes back green and quieter than it claims to be. A
point asked about on one platform only is the same hole on the other.
"""

from pathlib import Path
import re
import sys

PLATFORMS = ['linux', 'darwin']


def asked_in(paths):
    """The points those files ask about, whether through the escape hatch or
    from the wrapper of the call itself."""
    points = set()
    for path in paths:
        source = path.read_text()
        points |= set(re.findall(r'sys\.(?:check|pause)\("([^"]+)"\)', source))
        points |= set(re.findall(r'(?:failing|check)\("([^"]+)"\)', source))
    return points


def main():
    root = Path(__file__).resolve().parent.parent
    chaos = root / 'syscalls_chaos.go'
    declared = set(re.findall(r'^\t"([^"]+)":', chaos.read_text(), re.M))

    if not declared:
        sys.exit('no chaos points are declared')

    for platform in PLATFORMS:
        other = [p for p in PLATFORMS if p != platform]
        sources = [path for path in sorted(root.glob('*.go'))
                   if path.name != 'syscalls_plain.go'
                   and not any(path.name.endswith(f'_{name}.go') for name in other)]
        silent = sorted(declared - asked_in(sources))

        if silent:
            sys.exit(f'on {platform}, nothing ever asks about: ' + ', '.join(silent))

    print(f'{len(declared)} chaos points, every one asked about on {" and ".join(PLATFORMS)}')


if __name__ == '__main__':
    main()
