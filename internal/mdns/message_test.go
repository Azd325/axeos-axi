package mdns

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"reflect"
	"testing"
)

const browsed = "_axeos._sub._http._tcp.local"

var (
	subtype  = name{"_axeos", "_sub", "_http", "_tcp", "local"}
	instance = name{"Bitaxe Max 2.2 (ABCD)", "_http", "_tcp", "local"}
	host     = name{"bitaxe-example", "local"}
)

type rr struct {
	owner name
	typ   uint16
	ttl   uint32
	data  []byte
}

func wire(n name) []byte {
	var b []byte
	for _, label := range n {
		b = append(b, byte(len(label)))
		b = append(b, label...)
	}
	return append(b, 0)
}

func srvData(port uint16, target name) []byte {
	return append(binary.BigEndian.AppendUint16([]byte{0, 0, 0, 0}, port), wire(target)...)
}

func txtData(pairs ...string) []byte {
	var b []byte
	for _, pair := range pairs {
		b = append(b, byte(len(pair)))
		b = append(b, pair...)
	}
	return b
}

func packet(flags uint16, records ...rr) []byte {
	msg := make([]byte, headerSize)
	binary.BigEndian.PutUint16(msg[2:], flags)
	binary.BigEndian.PutUint16(msg[6:], uint16(len(records)))
	for _, r := range records {
		msg = append(msg, wire(r.owner)...)
		msg = binary.BigEndian.AppendUint16(msg, r.typ)
		msg = binary.BigEndian.AppendUint16(msg, classIN|0x8000)
		msg = binary.BigEndian.AppendUint32(msg, r.ttl)
		msg = binary.BigEndian.AppendUint16(msg, uint16(len(r.data)))
		msg = append(msg, r.data...)
	}
	return msg
}

func response(records ...rr) []byte { return packet(0x8400, records...) }

func miner() []rr {
	return []rr{
		{subtype, typePTR, 4500, wire(instance)},
		{instance, typeSRV, 120, srvData(80, host)},
		{instance, typeTXT, 4500, txtData("board=2.2", "Family=Max", "asic=BM1397", "asic_count=1", "fw_version=v2.15.3", "board=ignored", "")},
		{host, typeA, 120, []byte{192, 0, 2, 10}},
	}
}

func TestCollectsAdvertisedService(t *testing.T) {
	c := newCollector(browsed)
	c.add(response(miner()...))
	want := []Service{{
		Instance: "Bitaxe Max 2.2 (ABCD)",
		Host:     "bitaxe-example.local",
		Port:     80,
		Addrs:    []netip.Addr{netip.MustParseAddr("192.0.2.10")},
		Text:     map[string]string{"board": "2.2", "family": "Max", "asic": "BM1397", "asic_count": "1", "fw_version": "v2.15.3"},
	}}
	if got := c.services(); !reflect.DeepEqual(got, want) {
		t.Fatalf("%#v", got)
	}
	if pending := c.pending(); len(pending) != 0 {
		t.Fatalf("complete service still pending: %v", pending)
	}
}

func TestNamesMatchWithoutCase(t *testing.T) {
	records := miner()
	records[0].owner = name{"_AxeOS", "_SUB", "_http", "_TCP", "Local"}
	records[1].owner = name{"BITAXE MAX 2.2 (abcd)", "_HTTP", "_tcp", "local"}
	records[3].owner = name{"Bitaxe-Example", "LOCAL"}
	c := newCollector(browsed)
	c.add(response(records...))
	got := c.services()
	if len(got) != 1 || got[0].Instance != instance[0] || got[0].Port != 80 || len(got[0].Addrs) != 1 {
		t.Fatalf("%#v", got)
	}
}

func TestIgnoresOtherServicesQueriesAndGoodbyes(t *testing.T) {
	printer := name{"Example Printer", "_http", "_tcp", "local"}
	other := []rr{
		{name{"_http", "_tcp", "local"}, typePTR, 4500, wire(printer)},
		{name{"_printer", "_sub", "_http", "_tcp", "local"}, typePTR, 4500, wire(printer)},
		{printer, typeSRV, 120, srvData(80, name{"printer-example", "local"})},
		{name{"printer-example", "local"}, typeA, 120, []byte{192, 0, 2, 20}},
	}
	goodbye := miner()
	goodbye[0].ttl = 0
	for label, msg := range map[string][]byte{
		"other service": response(other...),
		"query":         packet(0, miner()...),
		"goodbye":       response(goodbye...),
	} {
		c := newCollector(browsed)
		c.add(msg)
		if got := c.services(); len(got) != 0 {
			t.Errorf("%s listed: %#v", label, got)
		}
	}
}

func TestResolvesAcrossPacketsInAnyOrder(t *testing.T) {
	records := miner()
	c := newCollector(browsed)
	c.add(response(records[3]))
	c.add(response(records[0]))
	if got, want := c.pending(), []question{{instance, typeSRV}, {instance, typeTXT}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pending=%v", got)
	}
	if got := c.services(); len(got) != 0 {
		t.Fatalf("service without SRV listed: %#v", got)
	}
	c.add(response(records[1]))
	if got, want := c.pending(), []question{{instance, typeTXT}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pending=%v", got)
	}
	got := c.services()
	if len(got) != 1 || len(got[0].Addrs) != 1 || got[0].Text != nil {
		t.Fatalf("%#v", got)
	}

	unresolved := newCollector(browsed)
	unresolved.add(response(records[:2]...))
	if got, want := unresolved.pending(), []question{{host, typeA}, {instance, typeTXT}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pending=%v", got)
	}
}

func TestCompressedNames(t *testing.T) {
	msg := make([]byte, headerSize)
	binary.BigEndian.PutUint16(msg[2:], 0x8400)
	binary.BigEndian.PutUint16(msg[6:], 2)
	service := len(msg) + len(wire(subtype[:2]))
	msg = append(msg, wire(subtype)...)
	msg = binary.BigEndian.AppendUint16(msg, typePTR)
	msg = binary.BigEndian.AppendUint16(msg, classIN)
	msg = binary.BigEndian.AppendUint32(msg, 4500)
	pointer := []byte{0xC0, byte(service - 1)}
	rdata := append(wire(instance[:1])[:len(instance[0])+1], pointer...)
	msg = binary.BigEndian.AppendUint16(msg, uint16(len(rdata)))
	instanceAt := len(msg)
	msg = append(msg, rdata...)
	msg = append(msg, 0xC0, byte(instanceAt))
	msg = binary.BigEndian.AppendUint16(msg, typeSRV)
	msg = binary.BigEndian.AppendUint16(msg, classIN)
	msg = binary.BigEndian.AppendUint32(msg, 120)
	target := srvData(8080, host)
	msg = binary.BigEndian.AppendUint16(msg, uint16(len(target)))
	msg = append(msg, target...)

	c := newCollector(browsed)
	c.add(msg)
	got := c.services()
	if len(got) != 1 || got[0].Instance != instance[0] || got[0].Host != "bitaxe-example.local" || got[0].Port != 8080 {
		t.Fatalf("%#v", got)
	}
}

func TestMalformedPacketsAreIgnored(t *testing.T) {
	loop := response(rr{subtype, typePTR, 4500, []byte{0xC0, 0}})
	binary.BigEndian.PutUint16(loop[len(loop)-2:], 0xC000|uint16(len(loop)-2))
	c := newCollector(browsed)
	c.add(loop)
	c.add(response(rr{subtype, typePTR, 4500, append(append([]byte{0x41}, bytes.Repeat([]byte{'a'}, 0x41)...), 0)}))
	c.add(response(rr{instance, typeSRV, 120, []byte{0, 0, 0}}, rr{host, typeA, 120, []byte{192, 0, 2}}))
	c.add(response(rr{host, typeA, 120, bytes.Repeat([]byte{1}, 16)}))
	if len(c.instances) != 0 || len(c.srv) != 0 || len(c.addrs) != 0 {
		t.Fatalf("malformed records accepted: %#v", c)
	}

	valid := response(miner()...)
	for size := range len(valid) {
		newCollector(browsed).add(valid[:size])
	}
	long := response(rr{subtype, typePTR, 4500, bytes.Repeat(append([]byte{63}, bytes.Repeat([]byte{'a'}, 63)...), 5)})
	c.add(append(long, 0))
	if len(c.instances) != 0 {
		t.Fatal("name above 255 octets accepted")
	}
}

func TestQueryEncoding(t *testing.T) {
	got := query([]question{{name{"_axeos", "local"}, typePTR}, {host, typeA}})
	want := []byte{0, 0, 0, 0, 0, 2, 0, 0, 0, 0, 0, 0}
	want = append(want, wire(name{"_axeos", "local"})...)
	want = append(want, 0, 12, 0, 1)
	want = append(want, wire(host)...)
	want = append(want, 0, 1, 0, 1)
	if !bytes.Equal(got, want) {
		t.Fatalf("%v", got)
	}
	if answers(got) != nil {
		t.Fatal("own query parsed as a response")
	}
}
