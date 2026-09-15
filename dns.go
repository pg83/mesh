package main

import (
	"encoding/binary"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
)

const (
	defaultDNSPort = 53
	dnsSuffix      = ".mesh."
	dnsTTL         = 60
	dnsAnswerCap   = 512
	dnsTypeA       = 1
	dnsTypePTR     = 12
	dnsTypeAAAA    = 28
	dnsTypeAny     = 255
	dnsClassIN     = 1
	rcodeOK        = 0
	rcodeName      = 3
	rcodeRefuse    = 5
)

type Zone struct {
	subnet  *net.IPNet
	forward map[string][4]byte
	reverse map[[4]byte]string
}

func newZone(peers []RegistryRecord, subnet *net.IPNet) *Zone {
	z := &Zone{subnet: subnet, forward: map[string][4]byte{}, reverse: map[[4]byte]string{}}

	for _, p := range peers {
		name := strings.ToLower(strings.TrimSuffix(p.Name, "."))

		if name == "" || strings.ContainsAny(name, ". \t") {
			continue
		}

		ip := parseIntip(p.Intip)

		z.forward[name+dnsSuffix] = ip
		z.reverse[ip] = name + dnsSuffix
	}

	return z
}

func (z *Zone) reply(query []byte) []byte {
	if len(query) < 12 || query[2]&0xf8 != 0 || binary.BigEndian.Uint16(query[4:]) != 1 {
		return nil
	}

	name, end, ok := dnsName(query, 12)

	if !ok || len(query) < end+4 {
		return nil
	}

	qtype, class := binary.BigEndian.Uint16(query[end:]), binary.BigEndian.Uint16(query[end+2:])
	out := make([]byte, 0, dnsAnswerCap)

	out = append(out, query[:end+4]...)
	out[2] = 0x84 | query[2]&1
	out[3] = 0
	binary.BigEndian.PutUint16(out[6:], 0)
	binary.BigEndian.PutUint16(out[8:], 0)
	binary.BigEndian.PutUint16(out[10:], 0)

	rcode, answers := z.lookup(strings.ToLower(name), qtype, class)

	out[3] = byte(rcode)

	for _, answer := range answers {
		out = append(out, 0xc0, 12)
		out = binary.BigEndian.AppendUint16(out, answer.kind)
		out = binary.BigEndian.AppendUint16(out, dnsClassIN)
		out = binary.BigEndian.AppendUint32(out, dnsTTL)
		out = binary.BigEndian.AppendUint16(out, uint16(len(answer.data)))
		out = append(out, answer.data...)
	}

	binary.BigEndian.PutUint16(out[6:], uint16(len(answers)))

	return out
}

type dnsAnswer struct {
	kind uint16
	data []byte
}

func (z *Zone) lookup(name string, qtype, class uint16) (int, []dnsAnswer) {
	if class != dnsClassIN {
		return rcodeRefuse, nil
	}

	if strings.HasSuffix(name, dnsSuffix) {
		ip, known := z.forward[name]

		if !known {
			return rcodeName, nil
		}

		if qtype == dnsTypeA || qtype == dnsTypeAny {
			return rcodeOK, []dnsAnswer{{kind: dnsTypeA, data: ip[:]}}
		}

		return rcodeOK, nil
	}

	if ip, ok := reverseName(name); ok && z.subnet.Contains(net.IP(ip[:])) {
		owner, known := z.reverse[ip]

		if !known {
			return rcodeName, nil
		}

		if qtype == dnsTypePTR || qtype == dnsTypeAny {
			return rcodeOK, []dnsAnswer{{kind: dnsTypePTR, data: encodeName(owner)}}
		}

		return rcodeOK, nil
	}

	return rcodeRefuse, nil
}

func dnsName(packet []byte, offset int) (string, int, bool) {
	var labels []string

	for {
		if offset >= len(packet) {
			return "", 0, false
		}

		size := int(packet[offset])

		offset++

		if size == 0 {
			break
		}

		if size > 63 || offset+size > len(packet) || len(labels) > 32 {
			return "", 0, false
		}

		labels = append(labels, string(packet[offset:offset+size]))
		offset += size
	}

	return strings.Join(labels, ".") + ".", offset, true
}

func encodeName(name string) []byte {
	out := []byte{}

	for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}

	return append(out, 0)
}

func reverseName(name string) ([4]byte, bool) {
	var ip [4]byte

	rest, ok := strings.CutSuffix(name, ".in-addr.arpa.")
	parts := strings.Split(rest, ".")

	if !ok || len(parts) != 4 {
		return ip, false
	}

	for i, part := range parts {
		octet, err := strconv.Atoi(part)

		if err != nil || octet < 0 || octet > 255 || strconv.Itoa(octet) != part {
			return ip, false
		}

		ip[3-i] = byte(octet)
	}

	return ip, true
}

func serviceAddress(subnet *net.IPNet) [4]byte {
	return [4]byte(subnet.IP.To4())
}

type DNSServer struct {
	node *Node
	port uint16
	zone atomic.Pointer[Zone]
}

func newDNSServer(n *Node, cfg *Config) *DNSServer {
	port := cfg.DnsPort

	if port == 0 {
		port = defaultDNSPort
	}

	if port < 1 || port > 65535 {
		throwFmt("bad dns port %d", port)
	}

	s := &DNSServer{node: n, port: uint16(port)}

	n.net.register(17, s.port)

	return s
}

func (s *DNSServer) run() {
	for _, ip := range s.node.net.addresses {
		conn := throw2(gonet.DialUDP(s.node.net.stack, &tcpip.FullAddress{NIC: stackNIC, Addr: tcpip.AddrFrom4(ip), Port: s.port}, nil, ipv4.ProtocolNumber))

		go s.node.loop("dns "+net.IP(ip[:]).String(), func() { s.serve(conn) })
	}

	for {
		view := s.node.currentSnapshot()

		s.zone.Store(newZone(view.registry.records(), s.node.subnet))
		time.Sleep(time.Second)
	}
}

func (s *DNSServer) serve(conn *gonet.UDPConn) {
	buf := make([]byte, dnsAnswerCap)

	for {
		size, from, err := conn.ReadFrom(buf)

		throw(err)

		if zone := s.zone.Load(); zone != nil {
			if answer := zone.reply(buf[:size]); answer != nil {
				throw2(conn.WriteTo(answer, from))
			}
		}
	}
}

func runDNS(address, control string) {
	base := "http://" + controlAddress(control)
	client := controlClient()
	conn := throw2(net.ListenPacket("udp", address))

	var zone atomic.Pointer[Zone]

	go func() {
		for {
			try(func() {
				var status Status

				response := throw2(client.Get(base + "/status"))

				defer response.Body.Close()

				throw(json.NewDecoder(response.Body).Decode(&status))

				_, subnet := throw3(net.ParseCIDR(status.Subnet))

				zone.Store(newZone(status.Registry, subnet))
			}).catch(func(*Exception) {})

			time.Sleep(3 * time.Second)
		}
	}()

	buf := make([]byte, dnsAnswerCap)

	for {
		size, from, err := conn.ReadFrom(buf)

		throw(err)

		if z := zone.Load(); z != nil {
			if answer := z.reply(buf[:size]); answer != nil {
				throw2(conn.WriteTo(answer, from))
			}
		}
	}
}
