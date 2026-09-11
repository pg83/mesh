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

			throw(fs.Parse(os.Args[2:]))
			newNode(loadConfig(*config), log).run()
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
  run -c config.json    run a node
  keygen                print a fresh key pair as JSON
  status -s socket      dump node status as JSON
`)
}
