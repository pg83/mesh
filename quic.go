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
	"sync/atomic"
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

func quicClient(addr, certPath string, duration time.Duration, slow bool) {
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

	input := bufio.NewReader(os.Stdin)

	throw2(input.ReadString('\n'))

	if slow {
		quicSlow(stream, input)

		return
	}

	var stopping atomic.Bool

	go quicBoundary(func() {
		throw2(input.ReadString('\n'))
		stopping.Store(true)
	})

	payload := make([]byte, 64<<10)
	reply := make([]byte, len(payload))

	throw2(rand.Read(payload))

	started := time.Now()

	throw(stream.SetDeadline(started.Add(duration + 15*time.Second)))

	rounds := uint64(0)
	lastReport := started

	for time.Since(started) < duration || !stopping.Load() {
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

		if time.Since(lastReport) >= time.Second {
			quicReport(map[string]any{"event": "progress", "bytes": rounds * uint64(len(payload)), "seconds": time.Since(started).Seconds()})
			lastReport = time.Now()
		}
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

func quicSlow(stream *quic.Stream, input *bufio.Reader) {
	payload := make([]byte, 16<<20)

	throw2(rand.Read(payload))

	started := time.Now()

	throw(stream.SetDeadline(started.Add(time.Minute)))

	finished := make(chan struct{})

	go quicBoundary(func() {
		if size := throw2(stream.Write(payload)); size != len(payload) {
			throwFmt("short QUIC write: %d", size)
		}

		close(finished)
	})

	select {
	case <-finished:
		throwFmt("slow QUIC reader did not cause backpressure")
	case <-time.After(2 * time.Second):
	}

	quicReport(map[string]any{"event": "blocked"})
	throw2(input.ReadString('\n'))

	reply := make([]byte, len(payload))

	throw2(io.ReadFull(stream, reply))

	if !bytes.Equal(payload, reply) {
		throwFmt("slow QUIC response mismatch")
	}

	<-finished
	throw(stream.Close())

	if extra := throw2(io.ReadAll(stream)); len(extra) != 0 {
		throwFmt("unexpected QUIC response: %d bytes", len(extra))
	}

	quicReport(map[string]any{"event": "done", "bytes": len(payload), "rounds": len(payload) / (64 << 10), "seconds": time.Since(started).Seconds()})
}

func main() {
	quicBoundary(func() {
		switch os.Args[1] {
		case "server":
			quicServer(os.Args[2], os.Args[3])
		case "client", "slow-client":
			quicClient(os.Args[2], os.Args[3], throw2(time.ParseDuration(os.Args[4])), os.Args[1] == "slow-client")
		default:
			throwFmt("expected server or client")
		}
	})
}
