package main

import (
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

type Session struct {
	local uint16
	peer  uint16
	send  cipher.AEAD
	recv  cipher.AEAD
}

func sessionCipher(shared, sender, receiver []byte) cipher.AEAD {
	info := append([]byte(protocol), sender...)

	info = append(info, receiver...)

	key := make([]byte, chacha20poly1305.KeySize)

	throw2(io.ReadFull(hkdf.New(sha256.New, shared, nil, info), key))

	return throw2(chacha20poly1305.New(key))
}

func newSession(local, peer *Peer, private []byte) *Session {
	shared := throw2(curve25519.X25519(private, peer.pub))

	return &Session{
		local: local.index,
		peer:  peer.index,
		send:  sessionCipher(shared, local.pub, peer.pub),
		recv:  sessionCipher(shared, peer.pub, local.pub),
	}
}

func nonce(packet []byte) []byte {
	return append(make([]byte, 0, chacha20poly1305.NonceSize), packet[:8]...)[:chacha20poly1305.NonceSize]
}

func (s *Session) seal(source Vertex, kind byte, inner []byte, id uint64) []byte {
	out := make([]byte, headerTransport, headerTransport+7+len(inner)+s.send.Overhead())

	binary.LittleEndian.PutUint64(out, headerWord(kind, id))

	out[8] = byte(s.local)

	body := append(appendVertex(nil, source), inner...)

	return s.send.Seal(out, nonce(out), body, out[:headerTransport])
}

func (s *Session) open(packet []byte) (Vertex, []byte, bool) {
	if len(packet) < headerTransport+s.recv.Overhead() {
		return Vertex{}, nil, false
	}

	inner, err := s.recv.Open(nil, nonce(packet), packet[headerTransport:], packet[:headerTransport])

	if err != nil {
		return Vertex{}, nil, false
	}

	source, body, ok := decodeVertex(inner)

	return source, body, ok && source.valid() && !source.isHost() && len(body) != 0
}
