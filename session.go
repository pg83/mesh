package main

import (
	"crypto/cipher"
	"crypto/rand"
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

	return throw2(chacha20poly1305.NewX(key))
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

func (s *Session) seal(source Vertex, inner []byte, id uint64) []byte {
	out := make([]byte, headerTransport, headerTransport+len(inner)+s.send.Overhead())

	out[0] = packetTransport

	if len(inner) > 0 && inner[0] == innerGraph {
		out[0] = packetGraph
	}

	if len(inner) > 0 && inner[0] == innerRegistry {
		out[0] = packetRegistry
	}

	binary.LittleEndian.PutUint16(out[1:], s.local)
	binary.LittleEndian.PutUint64(out[3:], id)
	throw2(rand.Read(out[11:headerTransport]))

	body := append(appendVertex(nil, source), inner...)

	return s.send.Seal(out, out[11:headerTransport], body, out[:headerTransport])
}

func (s *Session) open(packet []byte) (Vertex, []byte, bool) {
	if len(packet) < headerTransport+s.recv.Overhead() {
		return Vertex{}, nil, false
	}

	inner, err := s.recv.Open(nil, packet[11:headerTransport], packet[headerTransport:], packet[:headerTransport])

	if err != nil {
		return Vertex{}, nil, false
	}

	source, body, ok := decodeVertex(inner)

	return source, body, ok && source.valid() && !source.isHost() && len(body) != 0
}
