Record age on `/metrics` is now measured from the moment a record was applied instead of from the owner's packet counter, which only told when the owner had started. Vertices from registry addresses and channels are trusted without an empty-hash check, the Linux netlink subscription no longer parses messages it already filtered by group, and the test suite covers a WebSocket upgrade refused because two public endpoints share one bind, host and path.

This release uses mesh/13 and the release 18 graph format. Update peers together with releases before 18.
