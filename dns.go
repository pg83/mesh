package main

import (
	"encoding/binary"
	"encoding/json"
	"math/rand/v2"
	"net"
	"net/http"
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
	dnsPoolTTL     = 5
	dnsFreshness   = 5 * time.Second
	dnsAnswerCap   = 512
	dnsTypeA       = 1
	dnsTypePTR     = 12
	dnsTypeAAAA    = 28
	dnsTypeAny     = 255
	dnsClassIN     = 1
	rcodeOK        = 0
	rcodeServer    = 2
	rcodeName      = 3
	rcodeRefuse    = 5
)

type Zone struct {
	subnet  *net.IPNet
	forward map[string]*DNSRecord
	reverse map[[4]byte]string
	expires time.Time
}

type DNSRecord struct {
	ips [][4]byte
	ttl uint32
}

func dnsLabel(label string) bool {
	if len(label) == 0 || len(label) > 63 {
		return false
	}

	for _, c := range label {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}

	return true
}

func parseDNSRecords(records map[string][]string) map[string][]string {
	if len(records) == 0 {
		return nil
	}

	out := map[string][]string{}

	for owner, peers := range records {
		name := strings.ToLower(strings.TrimSuffix(owner, "."))

		if len(name+dnsSuffix)+1 > 255 {
			throwFmt("dns_records: name too long: %q", owner)
		}

		for i, label := range strings.Split(name, ".") {
			if !dnsLabel(label) && !(i == 0 && label == "*") {
				throwFmt("dns_records: invalid name %q", owner)
			}
		}

		if _, exists := out[name]; exists {
			throwFmt("dns_records: duplicate name %q", owner)
		}

		if len(peers) == 0 {
			throwFmt("dns_records: %s: no nodes", owner)
		}

		for _, peer := range peers {
			peer = strings.ToLower(strings.TrimSuffix(peer, "."))

			if !dnsLabel(peer) {
				throwFmt("dns_records: %s: invalid node %q", owner, peer)
			}

			out[name] = append(out[name], peer)
		}
	}

	return out
}

func (z *Zone) add(name string, record *DNSRecord) {
	z.forward[name] = record

	for name != "mesh." {
		_, name, _ = strings.Cut(name, ".")

		if _, exists := z.forward[name]; !exists {
			z.forward[name] = nil
		}
	}
}

func newZone(peers []RegistryRecord, subnet *net.IPNet, records map[string][]string, reachable map[uint16]bool) *Zone {
	z := &Zone{subnet: subnet, forward: map[string]*DNSRecord{"mesh.": nil}, reverse: map[[4]byte]string{}}
	hosts := map[string][][4]byte{}

	for _, p := range peers {
		name := strings.ToLower(strings.TrimSuffix(p.Name, "."))

		if name == "" || strings.ContainsAny(name, ". \t") {
			continue
		}

		ip := parseIntip(p.Intip)

		z.add(name+dnsSuffix, &DNSRecord{ips: [][4]byte{ip}, ttl: dnsTTL})
		z.reverse[ip] = name + dnsSuffix

		if reachable[p.Index] {
			hosts[name] = append(hosts[name], ip)
		}
	}

	for name, members := range records {
		record := &DNSRecord{ttl: dnsPoolTTL}
		seen := map[[4]byte]bool{}

		for _, member := range members {
			for _, ip := range hosts[member] {
				if !seen[ip] {
					record.ips = append(record.ips, ip)
					seen[ip] = true
				}
			}
		}

		z.add(name+dnsSuffix, record)
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
		out = binary.BigEndian.AppendUint32(out, answer.ttl)
		out = binary.BigEndian.AppendUint16(out, uint16(len(answer.data)))
		out = append(out, answer.data...)
	}

	binary.BigEndian.PutUint16(out[6:], uint16(len(answers)))

	return out
}

type DNSAnswer struct {
	kind uint16
	data []byte
	ttl  uint32
}

func (z *Zone) lookup(name string, qtype, class uint16) (int, []DNSAnswer) {
	if class != dnsClassIN {
		return rcodeRefuse, nil
	}

	if name == "mesh." || strings.HasSuffix(name, dnsSuffix) {
		if !z.expires.IsZero() && time.Now().After(z.expires) {
			return rcodeServer, nil
		}

		record, known := z.forward[name]

		if !known {
			for parent := name; parent != "mesh."; {
				_, parent, _ = strings.Cut(parent, ".")

				if _, exists := z.forward[parent]; exists {
					record, known = z.forward["*."+parent]

					break
				}
			}
		}

		if !known {
			return rcodeName, nil
		}

		if record == nil || (qtype != dnsTypeA && qtype != dnsTypeAny) {
			return rcodeOK, nil
		}

		if len(record.ips) == 0 {
			return rcodeServer, nil
		}

		answers := make([]DNSAnswer, 0, len(record.ips))

		for _, ip := range record.ips {
			answers = append(answers, DNSAnswer{kind: dnsTypeA, data: ip[:], ttl: record.ttl})
		}

		rand.Shuffle(len(answers), func(i, j int) { answers[i], answers[j] = answers[j], answers[i] })

		return rcodeOK, answers
	}

	if ip, ok := reverseName(name); ok && z.subnet.Contains(net.IP(ip[:])) {
		if !z.expires.IsZero() && time.Now().After(z.expires) {
			return rcodeServer, nil
		}

		owner, known := z.reverse[ip]

		if !known {
			return rcodeName, nil
		}

		if qtype == dnsTypePTR || qtype == dnsTypeAny {
			return rcodeOK, []DNSAnswer{{kind: dnsTypePTR, data: encodeName(owner), ttl: dnsTTL}}
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
}

func (s *DNSServer) update(view *Snapshot) {
	reachable := map[uint16]bool{s.node.cfg.Index: true}

	for dst := range view.hops {
		if isHostID(dst) {
			reachable[vertexOwner(dst)] = true
		}
	}

	s.zone.Store(newZone(view.registry.records(), s.node.subnet, s.node.cfg.DnsRecords, reachable))
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

	client.Timeout = 2 * time.Second

	conn := throw2(net.ListenPacket("udp", address))

	var zone atomic.Pointer[Zone]

	go func() {
		for {
			try(func() {
				var status Status

				started := time.Now()
				response := throw2(client.Get(base + "/status"))

				defer response.Body.Close()

				if response.StatusCode != http.StatusOK {
					throwFmt("control: %s", response.Status)
				}

				throw(json.NewDecoder(response.Body).Decode(&status))

				_, subnet := throw3(net.ParseCIDR(status.Subnet))
				reachable := map[uint16]bool{status.Index: true}

				for _, peer := range status.Registry {
					if len(status.Routes[udpVertex(net.ParseIP(peer.Intip), 0).string()]) != 0 {
						reachable[peer.Index] = true
					}
				}

				z := newZone(status.Registry, subnet, status.DnsRecords, reachable)

				z.expires = started.Add(dnsFreshness)
				zone.Store(z)
			}).catch(func(*Exception) {})

			time.Sleep(time.Second)
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
