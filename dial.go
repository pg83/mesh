package main

import (
	"math/rand/v2"
	"time"
)

const maxBackoff = time.Minute

type DialKey struct {
	source InterfaceAddress
	target uint32
	vertex Vertex
	wire   SocketAddress
}

type DialAttempt struct {
	key      DialKey
	id       uint32
	local    *LocalAddress
	target   Vertex
	wire     SocketAddress
	source   Vertex
	socket   *UDPSocket
	session  *Session
	channels []*ChannelIO
	pending  bool
	next     time.Time

	failures int
}

func backoff(failures int) time.Duration {
	delay := time.Second << min(failures, 6)

	if delay > maxBackoff {
		delay = maxBackoff
	}

	return delay*3/4 + time.Duration(rand.Int64N(int64(delay)/2))
}

type DialResult struct {
	attempt  *DialAttempt
	channels []*ChannelIO
}
