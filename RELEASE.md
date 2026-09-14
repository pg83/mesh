Mesh gains an embedded SSH server: `sshd: true` or `mesh run -sshd` serves SSH on the node's mesh address (port `sshd_port`, default 22) from a userspace TCP stack fed directly by decrypted mesh packets, so the server is unreachable from any system interface and needs no system sshd. The host key is the node key, any ring member's Ed25519 key may log in, `sshd_authorized_keys` adds more, and `user@` selects the account when mesh runs as root. Sessions support exec, shell with pty, env, window resize and exit status.

The module now requires Go 1.26.

This release uses mesh/13 and the release 18 graph format. Update peers together with releases before 18.
