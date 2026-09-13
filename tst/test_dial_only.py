#!/usr/bin/env python3
"""A node absent from the static endpoints bootstraps the link itself;
its discovered endpoint then carries traffic both ways."""

import lib


def test():
    with lib.Lab(["lab", "laptop"], {1: ["lab", "laptop"]}, statics=["lab"]) as lab:
        lab.wait_links("laptop", ["lab"])
        lab.wait_links("lab", ["laptop"])
        lab.wait_ping("laptop", "lab")
        lab.wait_ping("lab", "laptop")


lib.main(test)
