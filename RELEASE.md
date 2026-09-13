Endpoints now describe incoming listeners only. UDP, WS and WSS connections can be initiated from every eligible local interface even when the node has no listeners. Outgoing UDP uses ephemeral ports; accepted connections carry return traffic and registry updates.

The graph separates ingress endpoints from source vertices. UDP bindings preserve one-way links without waiting for a response. Explicit binds track interface appearance/removal, including loopback origins behind a WebSocket reverse proxy. Darwin no longer reserves a TCP port for each UDP listener.

Downloaded ephemeral configs contain no listeners or private key. The status description map is now `addresses`; the web graph distinguishes source vertices from host vertices.

This release uses `mesh/10` and requires peers to be updated together. Releases through 10 use an incompatible transport.
