package mdns

import (
	"encoding/binary"
	"net/netip"
	"slices"
	"strings"
)

type Service struct {
	Instance string
	Host     string
	Port     int
	Addrs    []netip.Addr
	Text     map[string]string
}

const (
	typeA   uint16 = 1
	typePTR uint16 = 12
	typeTXT uint16 = 16
	typeSRV uint16 = 33
	classIN uint16 = 1

	headerSize      = 12
	maxNameSize     = 254
	maxPointerJumps = 16
)

// A name keeps its labels apart because a DNS-SD instance label may contain dots.
type name []string

func (n name) key() string    { return strings.ToLower(strings.Join(n, "\x00")) }
func (n name) String() string { return strings.Join(n, ".") }

type question struct {
	name name
	typ  uint16
}

func (q question) key() string { return q.name.key() + "\x00" + string(rune(q.typ)) }

func query(questions []question) []byte {
	msg := make([]byte, headerSize, 512)
	binary.BigEndian.PutUint16(msg[4:], uint16(len(questions)))
	for _, q := range questions {
		for _, label := range q.name {
			msg = append(msg, byte(len(label)))
			msg = append(msg, label...)
		}
		msg = append(msg, 0)
		msg = binary.BigEndian.AppendUint16(msg, q.typ)
		msg = binary.BigEndian.AppendUint16(msg, classIN)
	}
	return msg
}

func readName(msg []byte, off int) (labels name, next int, ok bool) {
	next = -1
	jumps, size := 0, 0
	for {
		if off >= len(msg) {
			return nil, 0, false
		}
		n := int(msg[off])
		switch {
		case n == 0:
			if next < 0 {
				next = off + 1
			}
			return labels, next, true
		case n&0xC0 == 0xC0:
			if off+1 >= len(msg) || jumps == maxPointerJumps {
				return nil, 0, false
			}
			if next < 0 {
				next = off + 2
			}
			jumps++
			off = (n&0x3F)<<8 | int(msg[off+1])
		case n&0xC0 != 0:
			return nil, 0, false
		default:
			end := off + 1 + n
			size += n + 1
			if end > len(msg) || size > maxNameSize {
				return nil, 0, false
			}
			labels = append(labels, strings.ToValidUTF8(string(msg[off+1:end]), "�"))
			off = end
		}
	}
}

type record struct {
	name name
	typ  uint16
	data int
	size int
}

// Queries, goodbye records (TTL 0) and everything after a malformed record are dropped.
func answers(msg []byte) []record {
	if len(msg) < headerSize || msg[2]&0x80 == 0 {
		return nil
	}
	off := headerSize
	for range binary.BigEndian.Uint16(msg[4:]) {
		_, next, ok := readName(msg, off)
		if !ok || next+4 > len(msg) {
			return nil
		}
		off = next + 4
	}
	total := int(binary.BigEndian.Uint16(msg[6:])) + int(binary.BigEndian.Uint16(msg[8:])) + int(binary.BigEndian.Uint16(msg[10:]))
	var records []record
	for range total {
		owner, next, ok := readName(msg, off)
		if !ok || next+10 > len(msg) {
			return records
		}
		typ := binary.BigEndian.Uint16(msg[next:])
		class := binary.BigEndian.Uint16(msg[next+2:])
		ttl := binary.BigEndian.Uint32(msg[next+4:])
		size := int(binary.BigEndian.Uint16(msg[next+8:]))
		data := next + 10
		if data+size > len(msg) {
			return records
		}
		off = data + size
		if class&0x7FFF == classIN && ttl != 0 {
			records = append(records, record{owner, typ, data, size})
		}
	}
	return records
}

func text(rdata []byte) map[string]string {
	pairs := map[string]string{}
	for len(rdata) > 0 {
		n := int(rdata[0])
		if 1+n > len(rdata) {
			break
		}
		key, value, _ := strings.Cut(string(rdata[1:1+n]), "=")
		key = strings.ToLower(key)
		if _, seen := pairs[key]; key != "" && !seen {
			pairs[key] = strings.ToValidUTF8(value, "�")
		}
		rdata = rdata[1+n:]
	}
	return pairs
}

type target struct {
	host name
	port int
}

type collector struct {
	service   name
	instances []name
	known     map[string]bool
	srv       map[string]target
	txt       map[string]map[string]string
	addrs     map[string][]netip.Addr
}

func newCollector(service string) *collector {
	return &collector{
		service: strings.Split(strings.TrimSuffix(service, "."), "."),
		known:   map[string]bool{},
		srv:     map[string]target{},
		txt:     map[string]map[string]string{},
		addrs:   map[string][]netip.Addr{},
	}
}

func (c *collector) browse() question { return question{c.service, typePTR} }

func (c *collector) add(msg []byte) {
	for _, r := range answers(msg) {
		owner := r.name.key()
		rdata := msg[r.data : r.data+r.size]
		switch r.typ {
		case typePTR:
			instance, _, ok := readName(msg, r.data)
			if ok && owner == c.service.key() && len(instance) > 0 && !c.known[instance.key()] {
				c.known[instance.key()] = true
				c.instances = append(c.instances, instance)
			}
		case typeSRV:
			if r.size < 7 {
				continue
			}
			port := int(binary.BigEndian.Uint16(rdata[4:]))
			if host, _, ok := readName(msg, r.data+6); ok && len(host) > 0 {
				c.srv[owner] = target{host, port}
			}
		case typeTXT:
			c.txt[owner] = text(rdata)
		case typeA:
			if r.size != 4 {
				continue
			}
			if addr := netip.AddrFrom4([4]byte(rdata)); !slices.Contains(c.addrs[owner], addr) {
				c.addrs[owner] = append(c.addrs[owner], addr)
			}
		}
	}
}

func (c *collector) pending() []question {
	var questions []question
	for _, instance := range c.instances {
		key := instance.key()
		if t, ok := c.srv[key]; !ok {
			questions = append(questions, question{instance, typeSRV})
		} else if len(c.addrs[t.host.key()]) == 0 {
			questions = append(questions, question{t.host, typeA})
		}
		if _, ok := c.txt[key]; !ok {
			questions = append(questions, question{instance, typeTXT})
		}
	}
	return questions
}

func (c *collector) services() []Service {
	var services []Service
	for _, instance := range c.instances {
		t, ok := c.srv[instance.key()]
		if !ok {
			continue
		}
		addrs := slices.Clone(c.addrs[t.host.key()])
		slices.SortFunc(addrs, netip.Addr.Compare)
		services = append(services, Service{
			Instance: instance[0],
			Host:     t.host.String(),
			Port:     t.port,
			Addrs:    addrs,
			Text:     c.txt[instance.key()],
		})
	}
	return services
}
