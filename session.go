package main

import (
	"crypto/cipher"
	"encoding/binary"
	"net"
	"time"

	"github.com/flynn/noise"
	"golang.org/x/crypto/chacha20poly1305"
)

type Session struct {
	peer     uint16
	localID  uint32
	remoteID uint32
	send     cipher.AEAD
	recv     cipher.AEAD
	counter  uint64
	window   Window
	endpoint *net.UDPAddr
	created  time.Time
	lastRecv time.Time
	lastSend time.Time
}

type Handshake struct {
	peer    uint16
	localID uint32
	state   *noise.HandshakeState
	addr    *net.UDPAddr
	created time.Time
}

func aead(cs *noise.CipherState) cipher.AEAD {
	key := cs.UnsafeKey()

	return throw2(chacha20poly1305.New(key[:]))
}

func newSession(peer uint16, localID, remoteID uint32, send, recv *noise.CipherState, endpoint *net.UDPAddr, now time.Time) *Session {
	return &Session{
		peer:     peer,
		localID:  localID,
		remoteID: remoteID,
		send:     aead(send),
		recv:     aead(recv),
		endpoint: endpoint,
		created:  now,
		lastRecv: now,
		lastSend: now,
	}
}

func nonce(counter uint64) []byte {
	n := make([]byte, nonceSize)

	binary.LittleEndian.PutUint64(n[4:], counter)

	return n
}

func (s *Session) seal(inner []byte, now time.Time) []byte {
	out := make([]byte, headerTransport, headerTransport+len(inner)+s.send.Overhead())

	out[0] = packetTransport
	binary.LittleEndian.PutUint32(out[1:], s.remoteID)
	binary.LittleEndian.PutUint64(out[5:], s.counter)

	out = s.send.Seal(out, nonce(s.counter), inner, out[:headerTransport])
	s.counter++
	s.lastSend = now

	return out
}

func (s *Session) open(packet []byte) ([]byte, bool) {
	if len(packet) < headerTransport+s.recv.Overhead() {
		return nil, false
	}

	counter := binary.LittleEndian.Uint64(packet[5:])
	inner, err := s.recv.Open(nil, nonce(counter), packet[headerTransport:], packet[:headerTransport])

	if err != nil {
		return nil, false
	}

	if !s.window.accept(counter) {
		return nil, false
	}

	return inner, true
}
