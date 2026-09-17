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

var faults = map[string]error{
	"accept": syscall.EMFILE,

	"accept lost":         syscall.EINVAL,
	"interface addresses": syscall.EMFILE,
	"interface event":     syscall.ENOBUFS,
	"implicit socket":     syscall.EADDRNOTAVAIL,
	"interfaces":          syscall.EMFILE,
	"listen packet":       syscall.EADDRNOTAVAIL,
	"netstack":            syscall.EINVAL,
	"panic":               syscall.EIO,
	"routes":              syscall.EBUSY,
	"socket read":         syscall.EIO,

	"tun read":  syscall.EIO,
	"tun write": syscall.EIO,
	"udp write": syscall.EHOSTDOWN,
	"ws read":   syscall.ECONNRESET,
	"ws write":  syscall.EPIPE,
}

var pauses = map[string]Wait{
	"channel accept pause": {"interfaces", 1},
	"channel report pause": {"tick", 2},
	"dial pause":           {"tick", 2},
	"interface pause":      {"tick", 1},
}

const patience = 30 * time.Second

type Wait struct {
	mark  string
	times uint64
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
	mu       sync.Mutex
	passed   *sync.Cond
	marks    map[string]uint64
}

func newChaos() Syscalls {
	spec := os.Getenv("MESH_CHAOS")

	if spec == "" {
		return OS{}
	}

	c := &Chaos{seed: 1, rates: map[string]uint64{}, marks: map[string]uint64{}}

	c.passed = sync.NewCond(&c.mu)

	if seed := os.Getenv("MESH_CHAOS_SEED"); seed != "" {
		c.seed = uint64(throw2(strconv.ParseUint(seed, 10, 64)))
	}

	for _, item := range strings.Split(spec, ",") {
		if stripped, found := strings.CutPrefix(item, "-"); found {
			delete(c.rates, stripped)

			continue
		}

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

func (c *Chaos) reached(mark string) {
	c.mu.Lock()
	c.marks[mark]++
	c.mu.Unlock()
	c.passed.Broadcast()
}

func (c *Chaos) pause(what string) {
	call := c.due(what)

	if call == 0 {
		return
	}

	until := pauses[what]
	expired := false

	timer := time.AfterFunc(patience, func() {
		c.mu.Lock()
		expired = true
		c.passed.Broadcast()
		c.mu.Unlock()
	})

	defer timer.Stop()

	c.mu.Lock()

	defer c.mu.Unlock()

	target := c.marks[until.mark] + until.times

	slog.Warn("chaos", "at", what, "call", call, "waits for", until.times, "of", until.mark)

	for c.marks[until.mark] < target {
		if expired {
			slog.Warn("chaos gave up waiting", "at", what, "for", until.mark)

			return
		}

		c.passed.Wait()
	}
}

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
	if what == "panic" && c.due(what) != 0 {
		panic("chaos: a panic that is not ours")
	}

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

	if err := l.chaos.failing("accept lost"); err != nil {
		return nil, err
	}

	return l.Listener.Accept()
}
