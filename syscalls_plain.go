//go:build !meshchaos

package main

// Nothing is invented in this binary: every call goes where it always went.
var sys Syscalls = OS{}
