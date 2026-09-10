#!/usr/bin/env python3
"""Two segments joined by one node: a and b can never link directly, learn
each other only through r's gossip, and talk over the two-hop path."""

import time

import lib


def test():
    with lib.Lab(["a", "r", "b"], {1: ["a", "r"], 2: ["r", "b"]}) as lab:
        lab.wait_links("r", ["a", "b"])
        lab.wait_links("a", ["r"])
        lab.wait_links("b", ["r"])
        lab.wait_ping("a", "r")
        lab.wait_ping("b", "r")

        lab.wait_nodes("a", ["a", "r", "b"])
        lab.wait_route("a", "b", ["r", "b"])
        lab.wait_route("b", "a", ["r", "a"])
        lab.wait_ping("a", "b")
        lab.wait_ping("b", "a")

        time.sleep(5)
        lab.wait_links("a", ["r"], timeout=1)
        lab.wait_links("b", ["r"], timeout=1)


lib.main(test)
