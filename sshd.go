package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"filippo.io/edwards25519"
	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
)

const (
	defaultSSHPort = 22
	sshNIC         = 1
	sshQueue       = 256
)

type SSHServer struct {
	node       *Node
	ip         [4]byte
	port       uint16
	link       *channel.Endpoint
	stack      *stack.Stack
	config     *ssh.ServerConfig
	authorized map[string]bool
}

type PtyRequest struct {
	Term          string
	Cols, Rows    uint32
	Width, Height uint32
	Modes         string
}

type WindowChange struct {
	Cols, Rows    uint32
	Width, Height uint32
}

type EnvRequest struct {
	Name, Value string
}

type ExecRequest struct {
	Command string
}

type ExitStatus struct {
	Status uint32
}

type Shell struct {
	server  *SSHServer
	channel ssh.Channel
	user    string
	env     []string
	pty     *PtyRequest
	started bool

	mu      sync.Mutex
	window  *os.File
	process *os.Process
}

func newSSHServer(n *Node, cfg *Config, intip [4]byte) *SSHServer {
	port := cfg.SshdPort

	if port == 0 {
		port = defaultSSHPort
	}

	if port < 1 || port > 65535 {
		throwFmt("bad sshd port %d", port)
	}

	s := &SSHServer{node: n, ip: intip, port: uint16(port), authorized: map[string]bool{}}

	if cfg.SshdAuthorizedKeys != "" {
		s.loadAuthorizedKeys(cfg.SshdAuthorizedKeys)
	}

	signer := throw2(ssh.NewSignerFromKey(ed25519.NewKeyFromSeed(decodeKey(cfg.Key))))

	s.config = &ssh.ServerConfig{PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		return nil, s.authorize(conn.User(), key)
	}}

	s.config.AddHostKey(signer)

	s.stack = stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol},
	})

	s.link = channel.New(sshQueue, uint32(cfg.Mtu), "")

	netstackCheck("nic", s.stack.CreateNIC(sshNIC, s.link))

	address := tcpip.ProtocolAddress{Protocol: ipv4.ProtocolNumber, AddressWithPrefix: tcpip.AddrFrom4(intip).WithPrefix()}

	netstackCheck("address", s.stack.AddProtocolAddress(sshNIC, address, stack.AddressProperties{}))

	s.stack.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: sshNIC}})

	return s
}

func netstackCheck(what string, err tcpip.Error) {
	if err != nil {
		throwFmt("sshd %s: %v", what, err)
	}
}

func (s *SSHServer) loadAuthorizedKeys(path string) {
	file := throw2(os.Open(path))

	defer file.Close()

	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))

		throw(err)

		s.authorized[string(key.Marshal())] = true
	}

	throw(scanner.Err())
}

func (s *SSHServer) authorize(name string, key ssh.PublicKey) error {
	if os.Getuid() == 0 && name != currentUser() {
		if _, err := user.Lookup(name); err != nil {
			return exceptionf("unknown user %q", name).asError()
		}
	}

	if s.authorized[string(key.Marshal())] {
		return nil
	}

	if crypto, ok := key.(ssh.CryptoPublicKey); ok {
		if public, ok := crypto.CryptoPublicKey().(ed25519.PublicKey); ok {
			if point, err := new(edwards25519.Point).SetBytes(public); err == nil {
				montgomery := string(point.BytesMontgomery())

				for _, peer := range s.node.currentSnapshot().registry.byIndex {
					if string(peer.pub) == montgomery {
						return nil
					}
				}
			}
		}
	}

	return exceptionf("key is not a ring member").asError()
}

func currentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}

	return strconv.Itoa(os.Getuid())
}

func (s *SSHServer) accepts(packet []byte) bool {
	if !validIPv4(packet) || packet[9] != 6 || [4]byte(packet[16:20]) != s.ip {
		return false
	}

	head := int(packet[0]&15) * 4

	return len(packet) >= head+4 && binary.BigEndian.Uint16(packet[head+2:]) == s.port
}

func (s *SSHServer) inject(packet []byte) {
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(append([]byte(nil), packet...))})

	s.link.InjectInbound(ipv4.ProtocolNumber, pkt)
	pkt.DecRef()
}

func (s *SSHServer) run() {
	go s.node.loop("sshd egress", s.egress)

	listener := throw2(gonet.ListenTCP(s.stack, tcpip.FullAddress{NIC: sshNIC, Addr: tcpip.AddrFrom4(s.ip), Port: s.port}, ipv4.ProtocolNumber))

	for {
		conn := throw2(listener.Accept())

		go s.serve(conn)
	}
}

func (s *SSHServer) egress() {
	for {
		pkt := s.link.ReadContext(context.Background())
		data := pkt.ToBuffer()
		packet := data.Flatten()

		pkt.DecRef()

		if destination := ipDestination(packet); destination != nil {
			post(s.node.tunInbox.in, any(TunPacket{payload: packet, destination: destination}))
		}
	}
}

func (s *SSHServer) serve(conn net.Conn) {
	defer conn.Close()

	try(func() {
		server, channels, requests := throw4(ssh.NewServerConn(conn, s.config))

		defer server.Close()

		go ssh.DiscardRequests(requests)

		for request := range channels {
			if request.ChannelType() != "session" {
				throw(request.Reject(ssh.UnknownChannelType, "only sessions are supported"))

				continue
			}

			channel, incoming := throw3(request.Accept())
			shell := &Shell{server: s, channel: channel, user: server.User()}

			go shell.run(incoming)
		}
	}).catch(func(e *Exception) { s.node.log.Debug("ssh connection closed", "err", e) })
}

func (ss *Shell) run(requests <-chan *ssh.Request) {
	defer ss.channel.Close()

	try(func() {
		for request := range requests {
			ss.handle(request)
		}
	}).catch(func(e *Exception) { ss.server.node.log.Debug("ssh session failed", "err", e) })

	ss.mu.Lock()

	process := ss.process

	ss.mu.Unlock()

	if process != nil {
		syscall.Kill(-process.Pid, syscall.SIGHUP)
	}
}

func (ss *Shell) handle(request *ssh.Request) {
	ok := false

	switch request.Type {
	case "pty-req":
		if ss.pty == nil && !ss.started {
			ss.pty = &PtyRequest{}
			ok = ssh.Unmarshal(request.Payload, ss.pty) == nil
		}
	case "env":
		env := EnvRequest{}

		if ok = ssh.Unmarshal(request.Payload, &env) == nil && !ss.started; ok {
			ss.env = append(ss.env, env.Name+"="+env.Value)
		}
	case "window-change":
		change := WindowChange{}

		ss.mu.Lock()

		if ok = ssh.Unmarshal(request.Payload, &change) == nil && ss.window != nil; ok {
			throw(pty.Setsize(ss.window, &pty.Winsize{Rows: uint16(change.Rows), Cols: uint16(change.Cols), X: uint16(change.Width), Y: uint16(change.Height)}))
		}

		ss.mu.Unlock()
	case "shell":
		if ok = !ss.started; ok {
			ss.start(nil)
		}
	case "exec":
		command := ExecRequest{}

		if ok = ssh.Unmarshal(request.Payload, &command) == nil && !ss.started; ok {
			ss.start([]string{"-c", command.Command})
		}
	}

	if request.WantReply {
		throw(request.Reply(ok, nil))
	}
}

func (ss *Shell) start(args []string) {
	ss.started = true

	cmd := ss.command(args)

	go ss.wait(cmd)
}

func (ss *Shell) command(args []string) *exec.Cmd {
	account := ss.account()
	cmd := exec.Command(account.shell, args...)

	if args == nil {
		cmd.Args = []string{"-" + strings.TrimPrefix(account.shell[strings.LastIndex(account.shell, "/"):], "/")}
	}

	cmd.Dir = account.home
	cmd.Env = append([]string{"HOME=" + account.home, "USER=" + account.name, "LOGNAME=" + account.name, "SHELL=" + account.shell, "PATH=" + os.Getenv("PATH")}, ss.env...)

	if ss.pty != nil {
		cmd.Env = append(cmd.Env, "TERM="+ss.pty.Term)
	}

	if account.credential != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: account.credential}
	}

	return cmd
}

func (ss *Shell) wait(cmd *exec.Cmd) {
	defer ss.channel.Close()

	status := uint32(255)

	err := try(func() {
		if ss.pty != nil {
			window := throw2(pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(ss.pty.Rows), Cols: uint16(ss.pty.Cols), X: uint16(ss.pty.Width), Y: uint16(ss.pty.Height)}))

			defer func() {
				ss.mu.Lock()
				ss.window = nil
				ss.mu.Unlock()
				window.Close()
			}()

			ss.mu.Lock()
			ss.window, ss.process = window, cmd.Process
			ss.mu.Unlock()

			go io.Copy(window, ss.channel)

			drained := make(chan struct{})

			go func() { io.Copy(ss.channel, window); close(drained) }()

			defer func() {
				select {
				case <-drained:
				case <-time.After(time.Second):
				}
			}()
		} else {
			stdin := throw2(cmd.StdinPipe())

			cmd.Stdout = ss.channel
			cmd.Stderr = ss.channel.Stderr()
			cmd.SysProcAttr = sessionAttributes(cmd.SysProcAttr)

			throw(cmd.Start())

			ss.mu.Lock()
			ss.process = cmd.Process
			ss.mu.Unlock()

			go func() { io.Copy(stdin, ss.channel); stdin.Close() }()
		}

		status = exitStatus(cmd.Wait())
	})

	if err != nil {
		ss.server.node.log.Info("ssh command failed", "user", ss.user, "err", err)
	}

	ss.channel.SendRequest("exit-status", false, ssh.Marshal(ExitStatus{Status: status}))
}

func sessionAttributes(attributes *syscall.SysProcAttr) *syscall.SysProcAttr {
	if attributes == nil {
		attributes = &syscall.SysProcAttr{}
	}

	attributes.Setsid = true

	return attributes
}

func exitStatus(err error) uint32 {
	if err == nil {
		return 0
	}

	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() >= 0 {
		return uint32(exit.ExitCode())
	}

	return 255
}

type Account struct {
	name, home, shell string
	credential        *syscall.Credential
}

func (ss *Shell) account() Account {
	account := Account{name: currentUser(), home: os.Getenv("HOME"), shell: os.Getenv("SHELL")}

	if os.Getuid() == 0 && ss.user != account.name {
		u := throw2(user.Lookup(ss.user))
		uid := uint32(throw2(strconv.Atoi(u.Uid)))
		gid := uint32(throw2(strconv.Atoi(u.Gid)))
		groups := []uint32{}

		for _, group := range throw2(u.GroupIds()) {
			groups = append(groups, uint32(throw2(strconv.Atoi(group))))
		}

		account = Account{name: u.Username, home: u.HomeDir, shell: loginShell(u.Username), credential: &syscall.Credential{Uid: uid, Gid: gid, Groups: groups}}
	}

	if account.home == "" {
		account.home = "/"
	}

	if account.shell == "" {
		account.shell = loginShell(account.name)
	}

	return account
}

func loginShell(name string) string {
	if file, err := os.Open("/etc/passwd"); err == nil {
		defer file.Close()

		scanner := bufio.NewScanner(file)

		for scanner.Scan() {
			fields := strings.Split(scanner.Text(), ":")

			if len(fields) >= 7 && fields[0] == name && fields[6] != "" {
				return fields[6]
			}
		}
	}

	return "/bin/sh"
}
