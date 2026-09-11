//go:build meshquic

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"os"
	"time"

	"github.com/quic-go/quic-go"
)

func quicReport(fields map[string]any) {
	throw(json.NewEncoder(os.Stdout).Encode(fields))
}

func quicBoundary(body func()) {
	try(body).catch(func(e *Exception) {
		quicReport(map[string]any{"error": e.Error()})
		os.Exit(1)
	})
}

func quicServer(addr, certPath string) {
	pub, key := throw3(ed25519.GenerateKey(rand.Reader))

	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		DNSNames:     []string{"mesh-quic"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der := throw2(x509.CreateCertificate(rand.Reader, cert, cert, pub, key))

	throw(os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600))

	listener := throw2(quic.ListenAddr(addr, &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		NextProtos:   []string{"mesh-quic-test"},
	}, nil))

	quicReport(map[string]any{"event": "ready"})

	for {
		conn := throw2(listener.Accept(context.Background()))

		quicReport(map[string]any{"event": "connected", "peer": conn.RemoteAddr().String()})

		go quicBoundary(func() {
			stream := throw2(conn.AcceptStream(context.Background()))
			size := throw2(io.Copy(stream, stream))

			throw(stream.Close())
			quicReport(map[string]any{"event": "done", "peer": conn.RemoteAddr().String(), "bytes": size})
		})
	}
}

func quicClient(addr, certPath string, duration time.Duration) {
	roots := x509.NewCertPool()

	if !roots.AppendCertsFromPEM(throw2(os.ReadFile(certPath))) {
		throwFmt("invalid server certificate")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	defer cancel()

	conn := throw2(quic.DialAddr(ctx, addr, &tls.Config{
		RootCAs:    roots,
		ServerName: "mesh-quic",
		NextProtos: []string{"mesh-quic-test"},
	}, nil))

	defer conn.CloseWithError(0, "done")

	stream := throw2(conn.OpenStreamSync(ctx))

	quicReport(map[string]any{"event": "ready"})
	throw2(bufio.NewReader(os.Stdin).ReadString('\n'))

	payload := make([]byte, 64<<10)
	reply := make([]byte, len(payload))

	throw2(rand.Read(payload))

	started := time.Now()

	throw(stream.SetDeadline(started.Add(duration + 15*time.Second)))

	rounds := uint64(0)

	for time.Since(started) < duration {
		binary.LittleEndian.PutUint64(payload, rounds)

		written := throw2(stream.Write(payload))

		if written != len(payload) {
			throwFmt("short QUIC write: %d", written)
		}

		throw2(io.ReadFull(stream, reply))

		if !bytes.Equal(payload, reply) {
			throwFmt("QUIC response mismatch at round %d", rounds)
		}

		rounds++
	}

	throw(stream.Close())

	if extra := throw2(io.ReadAll(stream)); len(extra) != 0 {
		throwFmt("unexpected QUIC response: %d bytes", len(extra))
	}

	quicReport(map[string]any{
		"event":   "done",
		"rounds":  rounds,
		"bytes":   rounds * uint64(len(payload)),
		"seconds": time.Since(started).Seconds(),
	})
}

func main() {
	quicBoundary(func() {
		switch os.Args[1] {
		case "server":
			quicServer(os.Args[2], os.Args[3])
		case "client":
			quicClient(os.Args[2], os.Args[3], throw2(time.ParseDuration(os.Args[4])))
		default:
			throwFmt("expected server or client")
		}
	})
}
