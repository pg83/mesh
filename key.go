package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"

	"github.com/flynn/noise"
	"golang.org/x/crypto/curve25519"
)

var cipherSuite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashBLAKE2s)

const prologue = "mesh/1"

type KeyPair struct {
	Pub string `json:"pub"`
	Key string `json:"key"`
}

func keygen() {
	kp := throw2(cipherSuite.GenerateKeypair(rand.Reader))

	out := throw2(json.Marshal(KeyPair{
		Pub: base64.StdEncoding.EncodeToString(kp.Public),
		Key: base64.StdEncoding.EncodeToString(kp.Private),
	}))

	os.Stdout.Write(append(out, '\n'))
}

func loadKeyPair(cfg *Config) noise.DHKey {
	priv := decodeKey(cfg.Key)
	pub := throw2(curve25519.X25519(priv, curve25519.Basepoint))

	return noise.DHKey{Private: priv, Public: pub}
}
