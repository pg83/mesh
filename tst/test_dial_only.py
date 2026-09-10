#!/usr/bin/env python3
"""A node without static addresses is never dialed, only dials: the link
comes up from its side and carries traffic both ways."""

import lib


def test():
    with lib.Lab(["lab", "laptop"], {1: ["lab", "laptop"]}, statics=["lab"]) as lab:
        lab.wait_links("laptop", ["lab"])
        lab.wait_links("lab", ["laptop"])
        lab.wait_ping("laptop", "lab")
        lab.wait_ping("lab", "laptop")


lib.main(test)
