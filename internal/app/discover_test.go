package app

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Azd325/axeos-axi/internal/mdns"
)

type fakeBrowser struct {
	services []mdns.Service
	err      error
	calls    []string
}

func (f *fakeBrowser) Browse(_ context.Context, service string, wait time.Duration) ([]mdns.Service, error) {
	f.calls = append(f.calls, service+" "+wait.String())
	return f.services, f.err
}

func discoverer(t *testing.T, browser *fakeBrowser) *App {
	t.Helper()
	a := New(func(string) string { t.Fatal("discover read the environment"); return "" })
	a.Browser = browser
	return a
}

func advertisedMiners() []mdns.Service {
	return []mdns.Service{
		{
			Instance: "Bitaxe Max 2.2 (EF01)", Host: "bitaxe-other.local", Port: 8080,
			Addrs: []netip.Addr{netip.MustParseAddr("192.0.2.11")},
		},
		{
			Instance: "Bitaxe Gamma 601 (ABCD)", Host: "bitaxe-example.local", Port: 80,
			Addrs: []netip.Addr{netip.MustParseAddr("192.0.2.10"), netip.MustParseAddr("192.0.2.99")},
			Text:  map[string]string{"board": "601", "family": "Gamma", "asic": "BM1370", "asic_count": "1", "fw_version": "v2.15.3"},
		},
		{Instance: "Bitaxe Supra 401 (2345)", Host: "bitaxe-unresolved.local", Port: 80, Text: map[string]string{"family": "Supra", "asic_count": "many"}},
	}
}

func TestDiscoverListsAdvertisedMiners(t *testing.T) {
	browser := &fakeBrowser{services: advertisedMiners()}
	code, out := execute(t, discoverer(t, browser), "discover")
	want := `service: _axeos._sub._http._tcp.local
timeout_s: 3
count: 3
miners[3]{address,hostname,family,firmware}:
  192.0.2.10,bitaxe-example.local,Gamma,v2.15.3
  "192.0.2.11:8080",bitaxe-other.local,null,null
  bitaxe-unresolved.local,bitaxe-unresolved.local,Supra,null
help[2]: axeos-axi --host <address> for live mining health of one miner,"axeos-axi discover --fields address,hostname,board,asic,asic_count for hardware detail"
`
	if code != 0 || out != want {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if strings.Join(browser.calls, ",") != "_axeos._sub._http._tcp.local 3s" {
		t.Fatalf("calls=%v", browser.calls)
	}
	for _, private := range []string{"ABCD", "EF01", "2345", "instance"} {
		if strings.Contains(out, private) {
			t.Errorf("MAC suffix or instance in default output: %s", private)
		}
	}
}

func TestDiscoverFieldsAndTimeout(t *testing.T) {
	browser := &fakeBrowser{services: advertisedMiners()}
	code, out := execute(t, discoverer(t, browser), "--timeout=10", "discover", "--fields", "instance,port,asic_count,board,asic")
	want := `service: _axeos._sub._http._tcp.local
timeout_s: 10
count: 3
miners[3]{instance,port,asic_count,board,asic}:
  Bitaxe Gamma 601 (ABCD),80,1,"601",BM1370
  Bitaxe Max 2.2 (EF01),8080,null,null,null
  Bitaxe Supra 401 (2345),80,many,null,null
help[1]: axeos-axi --host <address> for live mining health of one miner
`
	if code != 0 || out != want {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if strings.Join(browser.calls, ",") != "_axeos._sub._http._tcp.local 10s" {
		t.Fatalf("calls=%v", browser.calls)
	}
}

func TestDiscoverEmptyStateIsDefinitive(t *testing.T) {
	a := discoverer(t, &fakeBrowser{})
	code, out := execute(t, a, "discover", "--fields", "address")
	if code != 0 || !strings.Contains(out, "count: 0\n") || !strings.Contains(out, "state: 0 AxeOS miners answered mDNS service type _axeos._sub._http._tcp.local within 3 s\n") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if strings.Contains(out, "miners[") || !strings.Contains(out, "axeos-axi discover --timeout 9 to wait longer") {
		t.Fatal(out)
	}
	code, out = execute(t, a, "discover", "--timeout", "60")
	if code != 0 || !strings.Contains(out, "within 60 s") || strings.Contains(out, "to wait longer") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestDiscoverRejectsInputBeforeBrowse(t *testing.T) {
	browser := &fakeBrowser{services: advertisedMiners()}
	a := discoverer(t, browser)
	for _, args := range [][]string{
		{"discover", "--host", "192.0.2.10"}, {"--host=192.0.2.10", "discover"}, {"discover", "--bogus"},
		{"discover", "--timeout"}, {"discover", "--timeout", "0"}, {"discover", "--timeout", "61"},
		{"discover", "--timeout", "2.5"}, {"discover", "--timeout=soon"}, {"discover", "extra"},
	} {
		code, out := execute(t, a, args...)
		if code != 2 || !strings.Contains(out, "valid flags: --timeout, --fields, --help, -v, -V, --version; commands: info, asic, stats, firmware, scoreboard, logs, discover") {
			t.Errorf("args=%v code=%d out=%s", args, code, out)
		}
	}
	code, out := execute(t, a, "discover", "--fields", "address,ssid")
	if code != 2 || !strings.Contains(out, "unknown field ssid") || !strings.Contains(out, "valid fields: address,hostname,family,firmware,board,asic,asic_count,port,instance") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	host, requests := miner(t, fixture(t, "info"))
	for _, args := range [][]string{{"--timeout", "5"}, {"info", "--timeout", "5"}, {"stats", "--timeout=abc"}} {
		code, out := execute(t, New(func(string) string { return host }), args...)
		if code != 2 || !strings.Contains(out, "unknown flag --timeout") || !strings.Contains(out, "valid flags: --host, --fields,") {
			t.Errorf("args=%v code=%d out=%s", args, code, out)
		}
	}
	if len(browser.calls) != 0 || len(requests()) != 0 {
		t.Fatalf("network use before usage validation: %v %v", browser.calls, requests())
	}
}

func TestDiscoverFailureIsStructured(t *testing.T) {
	code, out := execute(t, discoverer(t, &fakeBrowser{err: errors.New("no active multicast-capable IPv4 network interface")}), "discover")
	if code != 1 || !strings.HasPrefix(out, "error:\n  code: discovery_failed\n  message: no active multicast-capable IPv4 network interface\nhelp: ") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestDiscoverIsNamedWhereAHostIsNeeded(t *testing.T) {
	browser := &fakeBrowser{}
	a := discoverer(t, browser)
	code, out := execute(t, a, "discover", "--help")
	if code != 0 || !strings.Contains(out, "command: discover") || !strings.Contains(out, "--timeout <seconds>; default 3") || !strings.Contains(out, "examples[3]:") || strings.Contains(out, "--host") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	code, out = execute(t, a, "--help")
	if code != 0 || !strings.Contains(out, "commands: \"info, asic, stats, firmware, scoreboard, logs, discover, restart, tuning; ") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if len(browser.calls) != 0 {
		t.Fatalf("help browsed the network: %v", browser.calls)
	}

	code, out = execute(t, New(func(string) string { return "" }), "info")
	if code != 2 || !strings.Contains(out, "code: host_required") || !strings.Contains(out, "help: axeos-axi discover finds miners") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	host, _ := miner(t, fixture(t, "info"))
	code, out = execute(t, New(func(string) string { return host }))
	if code != 0 || !strings.Contains(out, "axeos-axi discover to find AxeOS miners on the local network") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}
