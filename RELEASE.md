Mesh now sends graph vertices and edges as separate authenticated packet types once per second. Vertex descriptions appear only once per publication. Edges with unknown vertices are discarded and can be accepted on a later publication after the descriptions arrive.

For the captured lab graph, this reduces one publication from 32,477 to 21,976 bytes of UDP payload. Connection selection, publication frequency and edge versions are unchanged.

This release uses mesh/11. Update peers together; releases through 12 use an incompatible wire format.
