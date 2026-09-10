package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"os"

	"github.com/flynn/noise"
	"golang.org/x/crypto/curve25519"
)

var cipherSuite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashBLAKE2s)

const prologue = "mesh/1"

type KeyPair struct {
	Key string `json:"key"`
	Pub string `json:"pub"`
	Sig string `json:"sig"`
}

func deriveKeys(seed []byte) (noise.DHKey, ed25519.PrivateKey) {
	h := sha512.Sum512(seed)
	scalar := h[:32]

	scalar[0] &= 248
	scalar[31] &= 127
	scalar[31] |= 64

	pub := throw2(curve25519.X25519(scalar, curve25519.Basepoint))

	return noise.DHKey{Private: scalar, Public: pub}, ed25519.NewKeyFromSeed(seed)
}

func keygen() {
	seed := make([]byte, ed25519.SeedSize)

	throw2(rand.Read(seed))

	dh, sig := deriveKeys(seed)

	out := throw2(json.Marshal(KeyPair{
		Key: base64.StdEncoding.EncodeToString(seed),
		Pub: base64.StdEncoding.EncodeToString(dh.Public),
		Sig: base64.StdEncoding.EncodeToString(sig.Public().(ed25519.PublicKey)),
	}))

	os.Stdout.Write(append(out, '\n'))
}
