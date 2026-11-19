package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const wifiScanBody = `{"networks":[{"ssid":"example-net-1","rssi":-48,"authmode":3},{"ssid":"example-net-2","rssi":-71,"authmode":0,"channel":6}]}`

func TestWifiScan(t *testing.T) {
	host, calls := scoreboardMiner(t, http.StatusOK, wifiScanBody)
	a := New(func(string) string { return host })
	code, out := execute(t, a, "wifi-scan", "--experimental")
	if code != 0 || !strings.HasPrefix(out, "count: 2\nnetworks[2]{rssi,authmode}:\n  -48,3\n  -71,0\nhelp[1]: ") {
		t.Fatalf("%d %s", code, out)
	}
	if strings.Contains(out, "example-net") || !strings.Contains(out, "--experimental --fields rssi,authmode,ssid") {
		t.Fatalf("default output names a network: %s", out)
	}
	code, out = execute(t, a, "wifi-scan", "--experimental", "--fields", "ssid,rssi")
	if code != 0 || out != "count: 2\nnetworks[2]{ssid,rssi}:\n  example-net-1,-48\n  example-net-2,-71\n" {
		t.Fatalf("%d %s", code, out)
	}
	code, out = execute(t, a, "wifi-scan", "--experimental", "--fields", "rssi,channel")
	if code != 2 || !strings.Contains(out, "unknown field channel") || !strings.Contains(out, "valid fields: rssi,authmode,ssid") {
		t.Fatalf("%d %s", code, out)
	}
	code, out = execute(t, a, "wifi-scan", "--experimental", "--json", "--fields", "authmode")
	if code != 0 || strings.Contains(out, "channel") || strings.Contains(out, "example-net") {
		t.Fatalf("%d %s", code, out)
	}
	if got := strings.Join(calls(), ","); got != "GET /api/system/wifi/scan,GET /api/system/wifi/scan,GET /api/system/wifi/scan" {
		t.Fatal(got)
	}
}

func TestWifiScanRefusedWithoutAcknowledgement(t *testing.T) {
	host, calls := scoreboardMiner(t, http.StatusOK, wifiScanBody)
	for _, args := range [][]string{{"wifi-scan"}, {"wifi-scan", "--fields", "ssid"}} {
		code, out := execute(t, New(func(string) string { return host }), args...)
		for _, want := range []string{"code: experimental_required", "--experimental", "more than 20 access points", "restart the miner", "https://github.com/bitaxeorg/ESP-Miner/pull/2029"} {
			if code != 2 || !strings.Contains(out, want) {
				t.Fatalf("want %q: %d %s", want, code, out)
			}
		}
	}
	if len(calls()) != 0 {
		t.Fatalf("requests before the acknowledgement: %v", calls())
	}
}

func TestWifiScanUnknownFieldSendsNoRequest(t *testing.T) {
	host, calls := scoreboardMiner(t, http.StatusOK, wifiScanBody)
	code, out := execute(t, New(func(string) string { return host }), "wifi-scan", "--experimental", "--fields", "rssi,bogus")
	if code != 2 || !strings.Contains(out, "unknown field bogus") || len(calls()) != 0 {
		t.Fatalf("%d %s %v", code, out, calls())
	}
}

func TestWifiScanResults(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status, code     int
	}{
		{"empty", `{"networks":[]}`, "count: 0\nstate: 0 networks found by the miner scan\n", http.StatusOK, 0},
		{"not found", "", "code: not_supported\n  message: wifi scan is not supported by this firmware\n", http.StatusNotFound, 1},
		{"root redirect", "", "code: not_supported\n  message: wifi scan is not supported by this firmware\n", http.StatusFound, 1},
		{"scan failed", "WiFi scan failed", "code: scan_failed\n  message: the miner reported that the Wi-Fi scan failed", http.StatusInternalServerError, 1},
		{"unauthorized", "", "code: miner_read_failed\n  message: miner returned HTTP 401 for wifi/scan\n", http.StatusUnauthorized, 1},
		{"no networks key", `{}`, "code: miner_read_failed\n  message: miner returned invalid JSON", http.StatusOK, 1},
		{"array", `[]`, "code: miner_read_failed\n  message: miner returned invalid JSON", http.StatusOK, 1},
		{"entry not an object", `{"networks":[7]}`, "code: invalid_wifi_scan\n", http.StatusOK, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host, calls := scoreboardMiner(t, tc.status, tc.body)
			code, out := execute(t, New(func(string) string { return host }), "wifi-scan", "--experimental")
			if code != tc.code || !strings.Contains(out, tc.want) {
				t.Fatalf("%d %s", code, out)
			}
			if tc.code == 0 && out != tc.want {
				t.Fatalf("%s", out)
			}
			if strings.Join(calls(), ",") != "GET /api/system/wifi/scan" {
				t.Fatal(calls())
			}
		})
	}
}

func TestExperimentalIsAFlagOfWifiScanOnly(t *testing.T) {
	var requests int
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer s.Close()
	for _, command := range []string{"info", "scoreboard", "restart", "firmware"} {
		code, out := execute(t, New(func(string) string { return s.URL }), command, "--experimental")
		if code != 2 || !strings.Contains(out, "unknown flag --experimental; it is a flag of `wifi-scan` only") {
			t.Fatalf("%s: %d %s", command, code, out)
		}
	}
	code, out := execute(t, New(func(string) string { return s.URL }), "wifi-scan", "--bogus")
	if code != 2 || !strings.Contains(out, "valid flags: --host, --fields, --experimental,") || requests != 0 {
		t.Fatalf("%d %s %d", code, out, requests)
	}
}
