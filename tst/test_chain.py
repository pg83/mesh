#!/usr/bin/env python3
"""A four node chain across three segments: the ends reach each other over a
three hop path, and the path shifts when a middle node drops out."""

import lib


def test():
    segments = {1: ["a", "m"], 2: ["m", "n"], 3: ["n", "b"]}

    with lib.Lab(["a", "m", "n", "b"], segments) as lab:
        lab.wait_links("a", ["m"])
        lab.wait_links("b", ["n"])

        lab.wait_nodes("a", ["a", "m", "n", "b"])
        lab.wait_route("a", "b", ["m", "n", "b"])
        lab.wait_ping("a", "b")
        lab.wait_ping("b", "a")

        lab.stop_node("n")
        lab.wait_route("a", "b", None, timeout=60)


lib.main(test)
