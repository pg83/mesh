#!/usr/bin/env python3
"""Three nodes on one segment: full mesh of links, ping every pair, a killed
node drops out of its peers within the session timeout and comes back."""

import lib


def test():
    with lib.Lab(["a", "b", "c"], {1: ["a", "b", "c"]}) as lab:
        lab.wait_links("a", ["b", "c"])
        lab.wait_links("b", ["a", "c"])
        lab.wait_links("c", ["a", "b"])

        for src in "abc":
            for dst in "abc":
                if src != dst:
                    lab.wait_ping(src, dst)

        lab.stop_node("c")
        lab.wait_links("a", ["b"], timeout=30)
        lab.wait_links("b", ["a"], timeout=30)

        lab.start_node("c")
        lab.wait_links("c", ["a", "b"])
        lab.wait_links("a", ["b", "c"])
        lab.wait_ping("a", "c")
        lab.wait_ping("c", "b")


lib.main(test)
