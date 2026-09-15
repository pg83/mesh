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
			sshd := fs.Bool("sshd", false, "serve SSH on the mesh address")
			sshdPort := fs.Int("sshd-port", 0, "SSH port on the mesh address (default 22)")
			sshdKeys := fs.String("sshd-authorized-keys", "", "extra authorized keys file for SSH")
			dns := fs.Bool("dns", false, "serve the mesh zone on the mesh address")
			dnsPort := fs.Int("dns-port", 0, "DNS port on the mesh address (default 53)")
			tun := fs.Bool("tun", false, "create the TUN device (the default without -sshd)")
			noTun := fs.Bool("no-tun", false, "no TUN device: the node only relays and serves SSH and DNS")

			throw(fs.Parse(os.Args[2:]))

			cfg := loadConfig(*config)

			if *keyFile != "" {
				cfg.Key = loadPrivateKey(*keyFile)
			}

			cfg.Sshd = cfg.Sshd || *sshd

			if *sshdPort != 0 {
				cfg.SshdPort = *sshdPort
			}

			if *sshdKeys != "" {
				cfg.SshdAuthorizedKeys = *sshdKeys
			}

			cfg.Dns = cfg.Dns || *dns

			if *dnsPort != 0 {
				cfg.DnsPort = *dnsPort
			}

			if *tun && *noTun {
				throwFmt("-tun and -no-tun exclude each other")
			}

			if *noTun {
				cfg.Tun = ""
			} else if cfg.Tun == "" && (*tun || !*sshd) {
				cfg.Tun = defaultTun
			}

			newNode(cfg, log).run()
		case "keygen":
			keygen()
		case "status":
			fs := flag.NewFlagSet("status", flag.ExitOnError)
			control := fs.String("control", "127.0.0.1:8058", "localhost control address")

			throw(fs.Parse(os.Args[2:]))
			showStatus(*control)
		case "web":
			fs := flag.NewFlagSet("web", flag.ExitOnError)
			control := fs.String("control", "127.0.0.1:8058", "localhost control address")
			listen := fs.String("listen", "127.0.0.1:8059", "web listen address")

			throw(fs.Parse(os.Args[2:]))
			go stopOnSignal(log)
			runWeb(*listen, *control)
		case "dns":
			fs := flag.NewFlagSet("dns", flag.ExitOnError)
			control := fs.String("control", "127.0.0.1:8058", "localhost control address")
			listen := fs.String("listen", "127.0.0.1:5355", "DNS listen address")

			throw(fs.Parse(os.Args[2:]))
			go stopOnSignal(log)
			runDNS(*listen, *control)
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
  run -c config.json [-key-file path] [-tun|-no-tun] [-sshd] [-dns]   run a node
  keygen                print a fresh key pair as JSON
  status [-control 127.0.0.1:8058]       dump node status as JSON
  web [-control 127.0.0.1:8058] [-listen 127.0.0.1:8059]
  dns [-control 127.0.0.1:8058] [-listen 127.0.0.1:5355]  serve the mesh zone from the node status
`)
}
