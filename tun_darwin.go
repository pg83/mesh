package main

import (
	"encoding/binary"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const defaultTun = "utun"

type Tun struct {
	file *os.File
}

func openTun(name string, intip [4]byte, subnet string, mtu int) *Tun {
	unit := 0

	if name != "utun" {
		if !strings.HasPrefix(name, "utun") {
			throwFmt("Darwin TUN name must be utun or utunN")
		}

		index := throw2(strconv.Atoi(strings.TrimPrefix(name, "utun")))

		if index < 0 || index > 65534 {
			throwFmt("bad utun index")
		}

		unit = index + 1
	}

	fd := throw2(unix.Socket(unix.AF_SYSTEM, unix.SOCK_DGRAM, 2))

	unix.CloseOnExec(fd)

	info := &unix.CtlInfo{}

	copy(info.Name[:], "com.apple.net.utun_control")
	throw(unix.IoctlCtlInfo(fd, info))
	throw(unix.Connect(fd, &unix.SockaddrCtl{ID: info.Id, Unit: uint32(unit)}))
	throw(unix.SetNonblock(fd, true))

	tun := &Tun{file: os.NewFile(uintptr(fd), "utun")}
	actual := throw2(unix.GetsockoptString(fd, 2, 2))
	ip := net.IP(intip[:]).String()

	darwinCommand("/sbin/ifconfig", actual, "inet", ip, ip, "netmask", "255.255.255.255", "mtu", strconv.Itoa(mtu), "up")
	darwinCommand("/sbin/route", "-n", "add", "-net", subnet, "-interface", actual)

	return tun
}

func darwinCommand(command string, args ...string) {
	output, err := exec.Command(command, args...).CombinedOutput()

	if err != nil {
		throwFmt("%s: %s: %s", command, err, output)
	}
}

func (t *Tun) read(buf []byte) []byte {
	for {
		n := throw2(t.file.Read(buf))

		if n >= 4 && binary.BigEndian.Uint32(buf[:4]) == unix.AF_INET {
			return buf[4:n]
		}
	}
}

func (t *Tun) write(packet []byte) {
	out := make([]byte, 4, len(packet)+4)

	binary.BigEndian.PutUint32(out, unix.AF_INET)
	throw2(t.file.Write(append(out, packet...)))
}
