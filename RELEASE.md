A pty session no longer receives SIGHUP when the client closes its input; the process ends on its own and the session is cleaned up when the client disconnects. Failures to start an SSH command are logged.

This release uses mesh/13 and the release 18 graph format. Update peers together with releases before 18.
