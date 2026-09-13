package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"

	"filippo.io/edwards25519"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/ssh"
)

const protocol = "mesh/9"

type DHKey struct {
	private []byte
	public  []byte
}

type KeyPair struct {
	Key string `json:"key"`
	Pub string `json:"pub"`
}

func loadPrivateKey(path string) string {
	data := strings.TrimSpace(string(throw2(os.ReadFile(path))))

	if !strings.HasPrefix(data, "-----BEGIN ") {
		return data
	}

	key := throw2(ssh.ParseRawPrivateKey([]byte(data)))
	private, ok := key.(*ed25519.PrivateKey)

	if !ok {
		throwFmt("SSH private key must be Ed25519")
	}

	return base64.StdEncoding.EncodeToString(private.Seed())
}

func publicKey(pub string) []byte {
	if !strings.HasPrefix(pub, "ssh-") {
		return decodeKey(pub)
	}

	key, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(pub))

	throw(err)

	if strings.TrimSpace(string(rest)) != "" {
		throwFmt("expected one SSH public key")
	}

	if key.Type() != ssh.KeyAlgoED25519 {
		throwFmt("SSH public key must be Ed25519")
	}

	public := key.(ssh.CryptoPublicKey).CryptoPublicKey().(ed25519.PublicKey)
	point := throw2(new(edwards25519.Point).SetBytes(public))

	return point.BytesMontgomery()
}

func deriveKey(seed []byte) DHKey {
	h := sha512.Sum512(seed)
	scalar := h[:32]

	scalar[0] &= 248
	scalar[31] &= 127
	scalar[31] |= 64

	pub := throw2(curve25519.X25519(scalar, curve25519.Basepoint))

	return DHKey{private: scalar, public: pub}
}

func keygen() {
	seed := make([]byte, ed25519.SeedSize)

	throw2(rand.Read(seed))

	dh := deriveKey(seed)

	out := throw2(json.Marshal(KeyPair{
		Key: base64.StdEncoding.EncodeToString(seed),
		Pub: base64.StdEncoding.EncodeToString(dh.public),
	}))

	os.Stdout.Write(append(out, '\n'))
}
