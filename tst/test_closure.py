#!/usr/bin/env python3
"""Address closure: only a is configured with a static address. b and c dial
a, learn each other from a's flooded advertisements, and link up directly."""

import lib


def test():
    with lib.Lab(["a", "b", "c"], {1: ["a", "b", "c"]}, statics=["a"]) as lab:
        lab.wait(lambda: lab.nodes["a"].index in lab.links("b"), "b sees a")
        lab.wait(lambda: lab.nodes["a"].index in lab.links("c"), "c sees a")

        lab.wait_links("b", ["a", "c"], timeout=60)
        lab.wait_links("c", ["a", "b"], timeout=60)

        lab.wait_route("b", "c", ["c"])
        lab.wait_ping("b", "c")
        lab.wait_ping("c", "b")


lib.main(test)
