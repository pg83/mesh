//go:build meshchaos

package main

import (
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// What this build is allowed to invent, and the name the kernel gives it. A
// point that is not listed here cannot be armed, so a typo in the environment
// stops the node instead of quietly testing nothing.
var faults = map[string]error{
	"accept":              syscall.EMFILE,
	"interface addresses": syscall.EMFILE,
	"interface event":     syscall.ENOBUFS,
	"implicit socket":     syscall.EADDRNOTAVAIL,
	"interfaces":          syscall.EMFILE,
	"listen packet":       syscall.EADDRNOTAVAIL,
	"netstack":            syscall.EINVAL,
	"routes":              syscall.EBUSY,
	"socket read":         syscall.EIO,
	"tun read":            syscall.EIO,
	"tun write":           syscall.EIO,
	"udp write":           syscall.EHOSTDOWN,
	"ws read":             syscall.ECONNRESET,
	"ws write":            syscall.EPIPE,
}

// The same arming, but what comes of it is a wait rather than a refusal. Long
// enough that whatever else the node is doing gets there first, short enough
// that a scenario can afford a few of them.
var pauses = map[string]time.Duration{
	"dial pause":      time.Second,
	"interface pause": 300 * time.Millisecond,
}

func armChaos() {
	sys = newChaos()
}

type Chaos struct {
	OS
	seed     uint64
	rates    map[string]uint64
	calls    sync.Map
	announce sync.Once
}

// MESH_CHAOS names the points to arm and how often each fails: "tun read:50"
// refuses one call in fifty, "all:200" arms everything at that rate. It is
// every fiftieth call, not a call with one chance in fifty: a run that makes
// the calls gets the refusals rather than possibly getting none of them. The
// seed decides which of the fifty, never a clock and never a race, so the same
// seed breaks the same calls wherever the goroutines happen to run.
func newChaos() Syscalls {
	spec := os.Getenv("MESH_CHAOS")

	if spec == "" {
		return OS{}
	}

	c := &Chaos{seed: 1, rates: map[string]uint64{}}

	if seed := os.Getenv("MESH_CHAOS_SEED"); seed != "" {
		c.seed = uint64(throw2(strconv.ParseUint(seed, 10, 64)))
	}

	for _, item := range strings.Split(spec, ",") {
		name, rate := item, uint64(100)

		if at := strings.LastIndex(item, ":"); at >= 0 {
			name, rate = item[:at], throw2(strconv.ParseUint(item[at+1:], 10, 64))
		}

		if rate == 0 {
			throwFmt("chaos point %q needs a rate above zero", name)
		}

		if name == "all" {
			for point := range faults {
				c.rates[point] = rate
			}

			for point := range pauses {
				c.rates[point] = rate
			}

			continue
		}

		_, refuses := faults[name]
		_, waits := pauses[name]

		if !refuses && !waits {
			throwFmt("unknown chaos point %q", name)
		}

		c.rates[name] = rate
	}

	return c
}

// Whether this call of this point is the one to be interfered with.
func (c *Chaos) due(what string) uint64 {
	rate := c.rates[what]

	if rate == 0 {
		return 0
	}

	counter, _ := c.calls.LoadOrStore(what, &atomic.Uint64{})
	call := counter.(*atomic.Uint64).Add(1)

	c.announce.Do(func() { slog.Warn("chaos armed", "seed", c.seed, "points", len(c.rates)) })

	if (call+mix(c.seed, what, 0))%rate != 0 {
		return 0
	}

	return call
}

func (c *Chaos) failing(what string) error {
	call := c.due(what)

	if call == 0 {
		return nil
	}

	slog.Warn("chaos", "at", what, "call", call, "err", faults[what])

	return faults[what]
}

func (c *Chaos) pause(what string) {
	call := c.due(what)

	if call == 0 {
		return
	}

	slog.Warn("chaos", "at", what, "call", call, "waits", pauses[what])
	time.Sleep(pauses[what])
}

// Where in the count of a point its refusals fall. Same seed, same places,
// whatever order the goroutines happen to run in.
func mix(seed uint64, what string, call uint64) uint64 {
	hash := seed ^ 14695981039346656037

	for _, b := range append([]byte(what), byte(call), byte(call>>8), byte(call>>16), byte(call>>24)) {
		hash = (hash ^ uint64(b)) * 1099511628211
	}

	return hash >> 7
}

func (c *Chaos) interfaces() ([]net.Interface, error) {
	if err := c.failing("interfaces"); err != nil {
		return nil, err
	}

	return c.OS.interfaces()
}

func (c *Chaos) addresses(iface net.Interface) ([]net.Addr, error) {
	if err := c.failing("interface addresses"); err != nil {
		return nil, err
	}

	return c.OS.addresses(iface)
}

func (c *Chaos) listenPacket(config net.ListenConfig, network, address string) (net.PacketConn, error) {
	if err := c.failing("listen packet"); err != nil {
		return nil, err
	}

	return c.OS.listenPacket(config, network, address)
}

func (c *Chaos) readSocket(socket *UDPSocket, buf []byte) (int, net.IP, net.Addr, error) {
	if err := c.failing("socket read"); err != nil {
		return 0, nil, nil, err
	}

	return c.OS.readSocket(socket, buf)
}

func (c *Chaos) interfaceEvent(socket *os.File, buf []byte) error {
	if err := c.failing("interface event"); err != nil {
		return err
	}

	return c.OS.interfaceEvent(socket, buf)
}

func (c *Chaos) accepts(listener net.Listener) net.Listener {
	return ChaosListener{Listener: listener, chaos: c}
}

func (c *Chaos) check(what string) error {
	return c.failing(what)
}

type ChaosListener struct {
	net.Listener
	chaos *Chaos
}

func (l ChaosListener) Accept() (net.Conn, error) {
	if err := l.chaos.failing("accept"); err != nil {
		return nil, err
	}

	return l.Listener.Accept()
}
