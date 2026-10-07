package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
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

func listFixture(t *testing.T, name string) []any {
	t.Helper()
	b, err := os.ReadFile("../axeos/testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var list []any
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatal(err)
	}
	return list
}

func miner(t *testing.T, info map[string]any) (string, func() []string) {
	t.Helper()
	responses := map[string]any{"info": info, "asic": fixture(t, "asic"), "statistics": fixture(t, "statistics"), "firmware/checksum": fixture(t, "firmware_checksum"), "scoreboard": listFixture(t, "scoreboard")}
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
		{"firmware", "partition: ota_1\nversion: v2.15.3\nsize_bytes: 1638400\nsha256: 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08\n", []string{"GET /api/system/firmware/checksum"}},
		{"scoreboard", "count: 3\nshares[3]{rank,difficulty,ntime}:\n  1,42896578860.4,1759000000\n  2,634703787.2,1759100000\n  3,9120344.5,1759200000\nhelp[1]: ", []string{"GET /api/system/scoreboard"}},
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
				if !strings.Contains(out, "axeos-axi firmware --host '"+host+"'") {
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
	if code != 2 || !strings.Contains(out, "unknown_field") || !strings.Contains(out, "axeos-axi info --help; valid fields: ") {
		t.Fatal(out)
	}
	code, out = execute(t, a, "--host", host, "--fields", "nonexistent")
	if code != 2 || !strings.Contains(out, "axeos-axi --help; valid fields: ") {
		t.Fatal(out)
	}
}

func TestUnknownFieldListsViewFields(t *testing.T) {
	host, _ := miner(t, fixture(t, "info"))
	a := New(func(string) string { return "http://invalid.example" })
	cases := []struct{ command, fields string }{
		{"", "hostname,model,firmware,hashrate,temperature,power_w,efficiency,fan,pool,pool_connection,pool_fallback,shares,best_difficulty,uptime_s,overheat,paused"},
		{"info", "hostname,asic_model,firmware,axeos_version,idf_version,board,heap_free_bytes,heap_internal_free_bytes,heap_min_free_bytes,heap_max_alloc_bytes,wifi_state,wifi_signal_dbm,uptime_s,reset_reason,partition"},
		{"asic", "model,device_model,asic_count,hash_domains,frequency_mhz,frequency_actual_mhz,frequency_default_mhz,core_voltage_set_mv,core_voltage_actual_mv,core_voltage_default_mv,fan_mode,fan_manual_pct,temperature_target_c"},
		{"stats", "sample_count,logging_interval_s,current_timestamp_ms,latest_timestamp_ms,hashrate_ghs,hashrate_1h_ghs,chip_temperature_c,regulator_temperature_c,power_w,state"},
		{"firmware", "partition,version,size_bytes,sha256"},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			base := []string{"--host", host}
			if tc.command != "" {
				base = append([]string{tc.command}, base...)
			}
			code, out := execute(t, a, append(append([]string{}, base...), "--fields", "nonexistent")...)
			want := "valid fields: " + tc.fields + " (or exact API field names)"
			if code != 2 || !strings.Contains(out, "unknown field nonexistent") || !strings.Contains(out, want) {
				t.Fatalf("code=%d %s", code, out)
			}
			for _, name := range strings.Split(tc.fields, ",") {
				code, out := execute(t, a, append(append([]string{}, base...), "--fields", name)...)
				if code == 2 {
					t.Errorf("listed field %s rejected: %s", name, out)
				}
			}
		})
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

func TestRepeatedHostIsAUsageError(t *testing.T) {
	first, firstCalls := miner(t, fixture(t, "info"))
	second, secondCalls := miner(t, fixture(t, "info"))
	a := New(func(string) string { return "" })
	for _, args := range [][]string{
		{"info", "--host", first, "--host", second},
		{"info", "--host=" + first, "--host=" + second},
		{"info", "--host", first, "--host=" + second},
		{"info", "--host=" + first, "--host", second},
		{"--host", first, "--host", second},
		{"--host", first, "info", "--host", first},
		{"logs", "--host", first, "--host", second},
	} {
		code, out := execute(t, a, args...)
		if code != 2 || !strings.Contains(out, "code: usage") || !strings.Contains(out, "--host was given more than once; a command takes one miner") || !strings.Contains(out, "valid flags:") {
			t.Errorf("args=%v code=%d out=%s", args, code, out)
		}
	}
	if len(firstCalls()) != 0 || len(secondCalls()) != 0 {
		t.Fatalf("requests after a repeated --host: %v %v", firstCalls(), secondCalls())
	}
}

func TestOneHostFlagOverridesTheEnvironment(t *testing.T) {
	fromEnv, envCalls := miner(t, fixture(t, "info"))
	fromFlag, flagCalls := miner(t, fixture(t, "info"))
	code, out := execute(t, New(func(string) string { return fromEnv }), "info", "--host", fromFlag)
	if code != 0 {
		t.Fatalf("code=%d %s", code, out)
	}
	if len(envCalls()) != 0 || !slices.Equal(flagCalls(), []string{"GET /api/system/info"}) {
		t.Fatalf("env=%v flag=%v", envCalls(), flagCalls())
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
	for _, command := range []string{"", "info", "asic", "stats", "firmware", "scoreboard"} {
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
	descriptionIndex, errorIndex := strings.Index(out, "\ndescription: "), strings.Index(out, "\nerror:")
	if !strings.HasPrefix(out, "bin: ") || descriptionIndex < 0 || descriptionIndex > errorIndex || !strings.Contains(out, "code: host_required") {
		t.Fatal(out)
	}
	code, out = execute(t, New(func(string) string { return "" }), "info")
	if code != 2 || strings.Contains(out, "bin:") || !strings.HasPrefix(out, "error:") || !strings.Contains(out, "code: host_required") {
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

func TestFirmwareChecksum(t *testing.T) {
	host, _ := miner(t, fixture(t, "info"))
	code, out := execute(t, New(func(string) string { return host }), "firmware", "--fields", "sha256,size")
	if code != 0 || out != "sha256: 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08\nsize: 1638400\n" {
		t.Fatalf("%d %s", code, out)
	}
	var requests []string
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
	}))
	defer old.Close()
	code, out = execute(t, New(func(string) string { return old.URL }), "firmware")
	if code != 1 || !strings.Contains(out, "code: not_supported") || !strings.Contains(out, "not supported by this firmware") || !strings.Contains(out, "axeos-axi info --host '"+old.URL+"'") {
		t.Fatalf("%d %s", code, out)
	}
	if strings.Join(requests, ",") != "GET /api/system/firmware/checksum" {
		t.Fatal(requests)
	}
	requests = nil
	older := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		http.Redirect(w, r, "/", http.StatusFound)
	}))
	defer older.Close()
	code, out = execute(t, New(func(string) string { return older.URL }), "firmware")
	if code != 1 || !strings.Contains(out, "code: not_supported") || !strings.Contains(out, "not supported by this firmware") {
		t.Fatalf("%d %s", code, out)
	}
	if strings.Join(requests, ",") != "GET /api/system/firmware/checksum" {
		t.Fatal(requests)
	}
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failing.Close()
	code, out = execute(t, New(func(string) string { return failing.URL }), "firmware")
	if code != 1 || !strings.Contains(out, "code: miner_read_failed") || !strings.Contains(out, "HTTP 500") {
		t.Fatalf("%d %s", code, out)
	}
}

func scoreboardMiner(t *testing.T, status int, body string) (string, func() []string) {
	t.Helper()
	var requests []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if status == http.StatusFound {
			http.Redirect(w, r, "/", status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s.URL, func() []string { return requests }
}

func TestScoreboard(t *testing.T) {
	host, _ := miner(t, fixture(t, "info"))
	a := New(func(string) string { return host })
	code, out := execute(t, a, "scoreboard")
	for _, optIn := range []string{"job_id", "extranonce2", "nonce", "6a1f03", "0000002a", "1A2B3C4D", "0034C000"} {
		if code != 0 || strings.Contains(strings.Split(out, "help[1]:")[0], optIn) {
			t.Errorf("default output carries %s: %d %s", optIn, code, out)
		}
	}
	if !strings.Contains(out, "axeos-axi scoreboard --host '"+host+"' --fields rank,difficulty,ntime,job_id,extranonce2,nonce,version_bits") {
		t.Fatal(out)
	}
	code, out = execute(t, a, "scoreboard", "--fields", "nonce,rank,job_id,since")
	if code != 2 || !strings.Contains(out, "unknown field since") || !strings.Contains(out, "valid fields: rank,difficulty,ntime,job_id,extranonce2,nonce,version_bits") {
		t.Fatalf("%d %s", code, out)
	}
	code, out = execute(t, a, "scoreboard", "--fields", "nonce,rank,job_id")
	if code != 0 || out != "count: 3\nshares[3]{nonce,rank,job_id}:\n  1A2B3C4D,1,6a1f03\n  00FFAA10,2,6a2b10\n  DEADBEEF,3,6a2c44\n" {
		t.Fatalf("%d %s", code, out)
	}
	for _, tc := range []struct {
		name, body, want string
		args             []string
		status, code     int
	}{
		{"empty", "[]", "count: 0\nstate: 0 shares recorded on the miner scoreboard\n", nil, http.StatusOK, 0},
		{"empty with fields", "[]", "count: 0\nstate: 0 shares recorded on the miner scoreboard\n", []string{"--fields", "nonce"}, http.StatusOK, 0},
		{"entry with its own rank", `[{"rank":0,"difficulty":5},{"rank":7,"difficulty":4}]`, "count: 2\nshares[2]{rank,difficulty}:\n  1,5\n  2,4\n", []string{"--fields", "rank,difficulty"}, http.StatusOK, 0},
		{"not found", "", "code: not_supported\n  message: scoreboard is not supported by this firmware\n", nil, http.StatusNotFound, 1},
		{"root redirect", "", "code: not_supported\n  message: scoreboard is not supported by this firmware\n", nil, http.StatusFound, 1},
		{"server error", "", "code: miner_read_failed\n  message: miner returned HTTP 500 for scoreboard\n", nil, http.StatusInternalServerError, 1},
		{"object", `{"difficulty":5}`, "code: miner_read_failed\n  message: miner returned invalid JSON", nil, http.StatusOK, 1},
		{"null", "null", "code: miner_read_failed\n  message: miner returned invalid JSON", nil, http.StatusOK, 1},
		{"entry not an object", `[{"difficulty":5},7]`, "code: invalid_scoreboard\n", nil, http.StatusOK, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host, calls := scoreboardMiner(t, tc.status, tc.body)
			code, out := execute(t, New(func(string) string { return host }), append([]string{"scoreboard"}, tc.args...)...)
			if code != tc.code || !strings.Contains(out, tc.want) {
				t.Fatalf("%d %s", code, out)
			}
			if tc.code == 0 && out != tc.want {
				t.Fatalf("%s", out)
			}
			if strings.Contains(out, "not_supported") && !strings.Contains(out, "axeos-axi info --host '"+host+"'") {
				t.Fatalf("%s", out)
			}
			if strings.Join(calls(), ",") != "GET /api/system/scoreboard" {
				t.Fatal(calls())
			}
		})
	}
}

func TestScoreboardUnknownFieldSendsNoRequest(t *testing.T) {
	host, calls := scoreboardMiner(t, http.StatusOK, "[]")
	code, out := execute(t, New(func(string) string { return host }), "scoreboard", "--fields", "rank,bogus")
	if code != 2 || !strings.Contains(out, "unknown field bogus") {
		t.Fatalf("%d %s", code, out)
	}
	if len(calls()) != 0 {
		t.Fatalf("requests before field validation: %v", calls())
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

func TestFirmwareHelpStatesMinimumFirmware(t *testing.T) {
	a := New(func(string) string { t.Fatal("offline command read environment"); return "" })
	code, out := execute(t, a, "firmware", "--help")
	if want := "needs firmware newer than v2.15.3, and v2.15.3 and older answer not_supported"; code != 0 || !strings.Contains(out, want) {
		t.Errorf("missing %q in %d %s", want, code, out)
	}
}

func TestConditionalFields(t *testing.T) {
	a := New(func(string) string { return "" })
	host, _ := miner(t, fixture(t, "info"))
	code, out := execute(t, a, "info", "--host", host, "--fields", "power_fault,blockHeight")
	if code != 0 || !strings.Contains(out, "power_fault: null") || !strings.Contains(out, "blockHeight: 970070") {
		t.Fatalf("code=%d %s", code, out)
	}
	info := fixture(t, "info")
	info["power_fault"] = "example fault"
	host, _ = miner(t, info)
	code, out = execute(t, a, "asic", "--host", host, "--fields", "power_fault")
	if code != 0 || !strings.Contains(out, "power_fault: example fault") {
		t.Fatalf("code=%d %s", code, out)
	}
	code, out = execute(t, a, "info", "--host", host, "--fields", "power_faults")
	if code != 2 || !strings.Contains(out, "unknown_field") {
		t.Fatalf("code=%d %s", code, out)
	}
}
