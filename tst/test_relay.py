#!/usr/bin/env python3
"""Two segments joined by one node: links follow the wires, a and b never
link directly. Traffic across r waits for gossip; only links are checked."""

import time

import lib


def test():
    with lib.Lab(["a", "r", "b"], {1: ["a", "r"], 2: ["r", "b"]}) as lab:
        lab.wait_links("r", ["a", "b"])
        lab.wait_links("a", ["r"])
        lab.wait_links("b", ["r"])
        lab.wait_ping("a", "r")
        lab.wait_ping("b", "r")

        time.sleep(5)
        lab.wait_links("a", ["r"], timeout=1)
        lab.wait_links("b", ["r"], timeout=1)


lib.main(test)
