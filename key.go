package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"os"

	"golang.org/x/crypto/curve25519"
)

const protocol = "mesh/4"

type DHKey struct {
	private []byte
	public  []byte
}

type KeyPair struct {
	Key string `json:"key"`
	Pub string `json:"pub"`
	Sig string `json:"sig"`
}

func deriveKeys(seed []byte) (DHKey, ed25519.PrivateKey) {
	h := sha512.Sum512(seed)
	scalar := h[:32]

	scalar[0] &= 248
	scalar[31] &= 127
	scalar[31] |= 64

	pub := throw2(curve25519.X25519(scalar, curve25519.Basepoint))

	return DHKey{private: scalar, public: pub}, ed25519.NewKeyFromSeed(seed)
}

func keygen() {
	seed := make([]byte, ed25519.SeedSize)

	throw2(rand.Read(seed))

	dh, sig := deriveKeys(seed)

	out := throw2(json.Marshal(KeyPair{
		Key: base64.StdEncoding.EncodeToString(seed),
		Pub: base64.StdEncoding.EncodeToString(dh.public),
		Sig: base64.StdEncoding.EncodeToString(sig.Public().(ed25519.PublicKey)),
	}))

	os.Stdout.Write(append(out, '\n'))
}
