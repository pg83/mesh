//go:build !meshprobe && !meshquic

package main

import (
	"flag"
	"log/slog"
	"os"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	try(func() {
		switch os.Args[1] {
		case "run":
			fs := flag.NewFlagSet("run", flag.ExitOnError)
			config := fs.String("c", "", "config file")
			keyFile := fs.String("key-file", "", "private key file (base64 seed or OpenSSH Ed25519)")

			throw(fs.Parse(os.Args[2:]))

			cfg := loadConfig(*config)

			if *keyFile != "" {
				cfg.Key = loadPrivateKey(*keyFile)
			}

			newNode(cfg, log).run()
		case "keygen":
			keygen()
		case "status":
			fs := flag.NewFlagSet("status", flag.ExitOnError)
			sock := fs.String("s", "", "status socket")

			throw(fs.Parse(os.Args[2:]))
			showStatus(*sock)
		default:
			printUsage()
			os.Exit(1)
		}
	}).catch(func(e *Exception) {
		log.Error("error", "err", e)
		os.Exit(1)
	})
}

func printUsage() {
	os.Stderr.WriteString(`Usage: mesh command [flags]

Commands:
  run -c config.json [-key-file path]    run a node
  keygen                print a fresh key pair as JSON
  status -s socket      dump node status as JSON
`)
}
