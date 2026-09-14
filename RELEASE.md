Mesh now exchanges the graph as one versioned record per node instead of separate edge and vertex packets. A record holds the node's listener and socket vertices with their attachment directions and the links it observes into them, and travels in one packet. Receivers keep only the newest version of each record, relay it unchanged, and derive the edge set from the records: a vertex dropped from a record disappears with every edge on it, so sockets of a previous incarnation cannot survive as zombies. The edge alive flag and per-edge versions are gone.

A record's version advances only when its content changes, so an idle network carries no graph updates for the graph actor to process.

This release uses mesh/13. Update peers together; releases through 17 use an incompatible graph format.
