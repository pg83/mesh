package main

import (
	"os"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

func openInterfaceEvents() *os.File {
	return interfaceFile(throw2(unix.Socket(unix.AF_ROUTE, unix.SOCK_RAW, unix.AF_UNSPEC)))
}

func readInterfaceEvent(socket *os.File, buf []byte) error {
	for {
		size, err := socket.Read(buf)

		if err != nil || interfaceEvent(buf[:size]) {
			return err
		}
	}
}

func interfaceEvent(data []byte) bool {
	messages, err := route.ParseRIB(0, data)

	if err != nil {
		return true
	}

	for _, msg := range messages {
		switch msg.(type) {
		case *route.InterfaceMessage, *route.InterfaceAddrMessage, *route.InterfaceAnnounceMessage:
			return true
		}
	}

	return false
}
