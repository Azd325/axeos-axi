package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func fixture(t *testing.T, name string) map[string]any {
	t.Helper()
	b, err := os.ReadFile("../axeos/testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func miner(t *testing.T, info map[string]any) (string, func() []string) {
	t.Helper()
	responses := map[string]map[string]any{"info": info, "asic": fixture(t, "asic"), "statistics": fixture(t, "statistics")}
	var mu sync.Mutex
	var requests []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.Method != http.MethodGet {
			t.Errorf("unexpected write request: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/system/")
		response, ok := responses[name]
		if !ok {
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(s.Close)
	return s.URL, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), requests...) }
}

func execute(t *testing.T, a *App, args ...string) (int, string) {
	t.Helper()
	var b bytes.Buffer
	code := a.Run(context.Background(), args, &b)
	return code, b.String()
}

func TestRecordedViews(t *testing.T) {
	for _, tc := range []struct {
		command, want string
		calls         []string
	}{
		{"", "hashrate: current=", []string{"GET /api/system/info"}},
		{"info", "board: \"601\"", []string{"GET /api/system/info"}},
		{"asic", "device_model: Gamma", []string{"GET /api/system/info", "GET /api/system/asic"}},
		{"stats", "sample_count: 3", []string{"GET /api/system/info", "GET /api/system/statistics"}},
	} {
		t.Run(tc.command, func(t *testing.T) {
			host, calls := miner(t, fixture(t, "info"))
			a := New(func(string) string { return host })
			args := []string{}
			if tc.command != "" {
				args = append(args, tc.command)
			}
			code, out := execute(t, a, args...)
			if code != 0 || !strings.Contains(out, tc.want) {
				t.Fatalf("code=%d output=%s", code, out)
			}
			if strings.Join(calls(), ",") != strings.Join(tc.calls, ",") {
				t.Fatalf("requests=%v", calls())
			}
			for _, private := range []string{"example-worker", "example-fallback", "example-wifi", "02:00:00:00:00:10", "example-bitcoin-address", "example-coinbase", "stratumUser:", "ssid:", "macAddr:"} {
				if strings.Contains(out, private) {
					t.Errorf("private value in default output: %s", private)
				}
			}
			if tc.command == "" {
				if !strings.HasPrefix(out, "bin:") || !strings.Contains(strings.Split(out, "\n")[1], "description:") {
					t.Fatal("home identity must precede data")
				}
				for _, field := range []string{"efficiency:", "pool_connection:", "rpm=4057\n", "shares: accepted=1908; rejected=11\n", "best_difficulty: all_time=42896578860; session=634703787\n", "overheat: false", "paused: false"} {
					if !strings.Contains(out, field) {
						t.Errorf("missing %s", field)
					}
				}
				if !strings.Contains(out, "--host '"+host+"'") {
					t.Fatal("home help lost explicit target")
				}
			}
		})
	}
}

func TestFieldsAndHostOverride(t *testing.T) {
	host, _ := miner(t, fixture(t, "info"))
	a := New(func(string) string { return "http://invalid.example" })
	code, out := execute(t, a, "--host="+host, "info", "--fields", "macAddr,stratumUser,ssid,pools")
	if code != 0 {
		t.Fatalf("code=%d %s", code, out)
	}
	if !strings.HasPrefix(out, "macAddr: \"02:00:00:00:00:10\"\nstratumUser: example-worker\nssid: example-wifi\n") {
		t.Fatal(out)
	}
	if !strings.Contains(out, "pools[2]{") || !strings.Contains(out, "example-worker") {
		t.Fatal("explicit nested pool fields unavailable")
	}
	code, out = execute(t, a, "asic", "--host", host, "--fields", "frequencyOptions,core_voltage_actual_mv")
	if code != 0 || !strings.Contains(out, "frequencyOptions[7]: 400,490,525,550,600,625,690") {
		t.Fatal(out)
	}
	code, out = execute(t, a, "stats", "--host", host, "--fields", "labels,statistics")
	if code != 0 || !strings.Contains(out, "statistics[3]:\n  - [19]:") {
		t.Fatal(out)
	}
	code, out = execute(t, a, "info", "--host", host, "--fields", "nonexistent")
	if code != 2 || !strings.Contains(out, "unknown_field") || !strings.Contains(out, "help: axeos-axi info --help;") {
		t.Fatal(out)
	}
	code, out = execute(t, a, "--host", host, "--fields", "nonexistent")
	if code != 2 || !strings.Contains(out, "help: axeos-axi --help;") {
		t.Fatal(out)
	}
}

func TestRejectInputBeforeNetwork(t *testing.T) {
	host, calls := miner(t, fixture(t, "info"))
	a := New(func(string) string { return host })
	for _, args := range [][]string{{"--hots", host}, {"info", "--json"}, {"reboot"}, {"asic", "extra"}, {"--host"}, {"--host="}, {"--fields", ""}, {"--fields", "a,a"}, {"--fields", "a,,b"}, {"info", "--help=yes"}, {"info", "--help", "--bad"}} {
		code, out := execute(t, a, args...)
		if code != 2 || !strings.Contains(out, "valid flags:") {
			t.Errorf("args=%v code=%d out=%s", args, code, out)
		}
	}
	if len(calls()) != 0 {
		t.Fatalf("requests before usage validation: %v", calls())
	}
}

func TestHelpAndVersionOffline(t *testing.T) {
	a := New(func(string) string { t.Fatal("offline command read environment"); return "" })
	a.Version = "1.2.3"
	for _, flag := range []string{"-v", "-V", "--version"} {
		code, out := execute(t, a, flag)
		if code != 0 || out != "1.2.3\n" {
			t.Fatalf("%s: %d %s", flag, code, out)
		}
	}
	for _, command := range []string{"", "info", "asic", "stats"} {
		args := []string{"--help"}
		if command != "" {
			args = append([]string{command}, args...)
		}
		code, out := execute(t, a, args...)
		if code != 0 || !strings.Contains(out, "examples[3]:") || !strings.Contains(out, "view_fields:") {
			t.Fatal(out)
		}
	}
}

func TestMissingHostAndUnreachable(t *testing.T) {
	code, out := execute(t, New(func(string) string { return "" }))
	if code != 2 || !strings.Contains(out, "--host") || !strings.Contains(out, "AXEOS_HOST") {
		t.Fatal(out)
	}
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	host := s.URL
	s.Close()
	code, out = execute(t, New(func(string) string { return host }), "info")
	if code != 1 || !strings.Contains(out, "miner unreachable") || strings.Contains(out, host) {
		t.Fatal(out)
	}
}

func TestStatisticsEmptyAndInvalid(t *testing.T) {
	for _, frequency := range []float64{0, 120} {
		info := fixture(t, "info")
		info["statsFrequency"] = frequency
		stats := fixture(t, "statistics")
		stats["statistics"] = []any{}
		fields, err := statsView(info, stats)
		var b bytes.Buffer
		if err != nil || write(&b, fields) != 0 {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), "sample_count: 0") {
			t.Fatal(b.String())
		}
		if frequency == 0 && !strings.Contains(b.String(), "logging disabled") {
			t.Fatal(b.String())
		}
	}
	info := fixture(t, "info")
	for _, stats := range []map[string]any{
		{}, {"labels": []any{"timestamp"}, "statistics": []any{[]any{}}},
		{"labels": []any{"timestamp", "timestamp"}, "statistics": []any{}},
		{"labels": []any{"timestamp"}, "statistics": []any{[]any{"invalid"}}},
	} {
		if _, err := statsView(info, stats); err == nil {
			t.Errorf("accepted malformed statistics: %v", stats)
		}
	}
	stats := fixture(t, "statistics")
	rows := stats["statistics"].([]any)
	rows[0], rows[2] = rows[2], rows[0]
	fields, err := statsView(info, stats)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fields {
		if f.Name == "latest_timestamp_ms" && f.Value != float64(612447553) {
			t.Errorf("latest sample selected by row order: %v", f.Value)
		}
	}
}

func TestDisabledStatisticsKeepsStatisticsFields(t *testing.T) {
	info := fixture(t, "info")
	info["statsFrequency"] = float64(0)
	host, calls := miner(t, info)
	code, out := execute(t, New(func(string) string { return host }), "stats", "--fields", "currentTimestamp,power_w")
	if code != 0 || !strings.Contains(out, "logging disabled") || !strings.Contains(out, "currentTimestamp: ") || !strings.Contains(out, "power_w: null") {
		t.Fatalf("%d %s", code, out)
	}
	if got := calls(); strings.Join(got, ",") != "GET /api/system/info,GET /api/system/statistics" {
		t.Fatal(got)
	}
}

func TestEfficiencyAndUnknowns(t *testing.T) {
	if got := efficiency(map[string]any{"power": float64(20), "hashRate": float64(1000)}); got != float64(20) {
		t.Fatal(got)
	}
	for _, m := range []map[string]any{nil, {"power": float64(20), "hashRate": float64(0)}, {"power": float64(-1), "hashRate": float64(1000)}} {
		if efficiency(m) != nil {
			t.Fatal("invalid rate produced efficiency")
		}
	}
	if flag(nil, "overheat_mode") != nil || measure(map[string]any{"temp": float64(-1)}, "temp", "C") != "unknown" {
		t.Fatal("missing state inferred as healthy")
	}
}
