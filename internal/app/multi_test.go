package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func statusMiner(t *testing.T, status int) (string, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var requests []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(s.Close)
	return s.URL, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), requests...) }
}

func slowMiner(t *testing.T) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(6 * time.Second):
		}
	}))
	t.Cleanup(s.Close)
	return s.URL
}

func noChecksumMiner(t *testing.T) (string, func() []string) {
	t.Helper()
	return statusMiner(t, http.StatusNotFound)
}

func renamed(t *testing.T, hostname string) map[string]any {
	info := fixture(t, "info")
	info["hostname"] = hostname
	return info
}

func hostArgs(hosts ...string) []string {
	var args []string
	for _, h := range hosts {
		args = append(args, "--host", h)
	}
	return args
}

func TestSeveralMinersPrintOneRowEach(t *testing.T) {
	first, firstCalls := miner(t, renamed(t, "miner-a"))
	second, secondCalls := miner(t, renamed(t, "miner-b"))
	a := New(func(string) string { return "" })
	code, out := execute(t, a, append([]string{"info", "--fields", "hostname,firmware"}, hostArgs(first, second)...)...)
	want := fmt.Sprintf("count: 2\nfailed: 0\nminers[2]{host,hostname,firmware,error}:\n  %q,miner-a,v2.15.3,null\n  %q,miner-b,v2.15.3,null\n", first, second)
	if code != 0 || out != want {
		t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
	}
	if !slices.Equal(firstCalls(), []string{"GET /api/system/info"}) || !slices.Equal(secondCalls(), []string{"GET /api/system/info"}) {
		t.Fatalf("requests %v %v", firstCalls(), secondCalls())
	}
}

func TestSeveralMinersUseTheDefaultViewColumns(t *testing.T) {
	first, _ := miner(t, renamed(t, "miner-a"))
	second, _ := miner(t, renamed(t, "miner-b"))
	a := New(func(string) string { return "" })
	cases := []struct{ command, header, row string }{
		{"", "hostname,model,firmware,hashrate,temperature,power_w,efficiency,fan,pool,pool_connection,pool_fallback,shares,best_difficulty,uptime_s,overheat,paused",
			"%s,miner-%s,BM1370,v2.15.3,current=1039.52 GH/s; 1h=1070.27 GH/s; expected=1071.00 GH/s,chip=65.12 C; regulator=73.00 C,20.1245117,19.36 J/TH,speed=60.74 %%; rpm=4057,pool.example.org,IPv4,false,accepted=1908; rejected=11,all_time=42896578860; session=634703787,612447,false,false,null"},
		{"info", "hostname,asic_model,firmware,axeos_version,idf_version,board,heap_free_bytes,heap_internal_free_bytes,heap_min_free_bytes,heap_max_alloc_bytes,wifi_state,wifi_signal_dbm,uptime_s,reset_reason,partition",
			"%s,miner-%s,BM1370,v2.15.3,Unified,v6.0.2,\"601\",7515284,85415,7091532,31744,Connected!,-49,612447,Software reset via esp_restart,ota_1,null"},
		{"asic", "model,device_model,asic_count,hash_domains,frequency_mhz,frequency_actual_mhz,frequency_default_mhz,core_voltage_set_mv,core_voltage_actual_mv,core_voltage_default_mv,fan_mode,fan_manual_pct,temperature_target_c",
			"%s,BM1370,Gamma,1,4,525,525,525,1100,1093,1150,auto,57,65,null"},
		{"stats", "sample_count,logging_interval_s,current_timestamp_ms,latest_timestamp_ms,hashrate_ghs,hashrate_1h_ghs,chip_temperature_c,regulator_temperature_c,power_w,state",
			"%s,3,120,612447946,612447553,979.0009766,1070.2735596,64.875,73,20.1245117,null,null"},
		{"firmware", "partition,version,size_bytes,sha256",
			"%s,ota_1,v2.15.3,1638400,9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08,null"},
	}
	for _, c := range cases {
		args := hostArgs(first, second)
		if c.command != "" {
			args = append([]string{c.command}, args...)
		}
		code, out := execute(t, a, args...)
		var row1, row2 string
		if c.command == "" || c.command == "info" {
			row1 = fmt.Sprintf(c.row, fmt.Sprintf("%q", first), "a")
			row2 = fmt.Sprintf(c.row, fmt.Sprintf("%q", second), "b")
		} else {
			row1 = fmt.Sprintf(c.row, fmt.Sprintf("%q", first))
			row2 = fmt.Sprintf(c.row, fmt.Sprintf("%q", second))
		}
		want := "count: 2\nfailed: 0\nminers[2]{host," + c.header + ",error}:\n  " + row1 + "\n  " + row2 + "\n"
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("command=%q code=%d out=%s\nwant %s", c.command, code, out, want)
		}
		if c.command == "" && (!strings.HasPrefix(out, "bin:") || !strings.Contains(out, "\nhelp[1]: \"axeos-axi info --host '"+first+"' --host '"+second+"'")) {
			t.Errorf("home output: %s", out)
		}
		if c.command != "" && strings.Contains(out, "help") {
			t.Errorf("a result without a failed miner has no help line: %s", out)
		}
	}
}

func TestFailedMinerKeepsItsRowAndTheOthersPrint(t *testing.T) {
	good, goodCalls := miner(t, renamed(t, "miner-a"))
	broken, brokenCalls := statusMiner(t, http.StatusInternalServerError)
	slow := slowMiner(t)
	a := New(func(string) string { return "" })
	slowTwo := slowMiner(t)
	started := time.Now()
	code, out := execute(t, a, append([]string{"info", "--fields", "hostname"}, hostArgs(slow, broken, good, slowTwo)...)...)
	elapsed := time.Since(started)
	want := fmt.Sprintf("count: 4\nfailed: 3\nminers[4]{host,hostname,error}:\n  %q,null,miner_read_failed\n  %q,null,miner_read_failed\n  %q,miner-a,null\n  %q,null,miner_read_failed\nhelp[3]: \"axeos-axi info --host '%s' for the error message of that miner\",\"axeos-axi info --host '%s' for the error message of that miner\",\"axeos-axi info --host '%s' for the error message of that miner\"\n", slow, broken, good, slowTwo, slow, broken, slowTwo)
	if code != 1 || out != want {
		t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
	}
	if elapsed > 6*time.Second {
		t.Fatalf("two slow miners took %s; the miners are not read at the same time", elapsed)
	}
	if !slices.Equal(brokenCalls(), []string{"GET /api/system/info"}) || !slices.Equal(goodCalls(), []string{"GET /api/system/info"}) {
		t.Fatalf("requests %v %v", brokenCalls(), goodCalls())
	}
}

func TestMinerWithoutChecksumPathIsNotSupported(t *testing.T) {
	good, _ := miner(t, fixture(t, "info"))
	old, oldCalls := noChecksumMiner(t)
	a := New(func(string) string { return "" })
	code, out := execute(t, a, append([]string{"firmware"}, hostArgs(good, old)...)...)
	want := fmt.Sprintf("count: 2\nfailed: 1\nminers[2]{host,partition,version,size_bytes,sha256,error}:\n  %q,ota_1,v2.15.3,1638400,9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08,null\n  %q,null,null,null,null,not_supported\nhelp[1]: \"axeos-axi firmware --host '%s' for the error message of that miner\"\n", good, old, old)
	if code != 1 || out != want {
		t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
	}
	if !slices.Equal(oldCalls(), []string{"GET /api/system/firmware/checksum"}) {
		t.Fatalf("requests %v", oldCalls())
	}
}

func TestEveryMinerFailed(t *testing.T) {
	first, _ := statusMiner(t, http.StatusInternalServerError)
	second, _ := statusMiner(t, http.StatusInternalServerError)
	a := New(func(string) string { return "" })
	code, out := execute(t, a, append([]string{"asic"}, hostArgs(first, second)...)...)
	if code != 1 || !strings.Contains(out, "count: 2\nfailed: 2\nminers[2]{host,model,") || strings.Count(out, "miner_read_failed\n") != 2 {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestSeveralMinersRequestsEqualASingleHostCall(t *testing.T) {
	for _, command := range []string{"", "info", "asic", "stats", "firmware"} {
		t.Run(command, func(t *testing.T) {
			alone, aloneCalls := miner(t, fixture(t, "info"))
			first, firstCalls := miner(t, fixture(t, "info"))
			second, secondCalls := miner(t, fixture(t, "info"))
			a := New(func(string) string { return "" })
			prefix := []string{}
			if command != "" {
				prefix = []string{command}
			}
			if code, out := execute(t, a, append(slices.Clone(prefix), "--host", alone)...); code != 0 {
				t.Fatalf("code=%d out=%s", code, out)
			}
			if code, out := execute(t, a, append(slices.Clone(prefix), hostArgs(first, second)...)...); code != 0 {
				t.Fatalf("code=%d out=%s", code, out)
			}
			if !slices.Equal(firstCalls(), aloneCalls()) || !slices.Equal(secondCalls(), aloneCalls()) {
				t.Fatalf("single %v, several %v %v", aloneCalls(), firstCalls(), secondCalls())
			}
		})
	}
}

func TestSeveralMinersWithFields(t *testing.T) {
	first, _ := miner(t, fixture(t, "info"))
	second, _ := statusMiner(t, http.StatusInternalServerError)
	a := New(func(string) string { return "" })
	code, out := execute(t, a, append([]string{"info", "--fields", "board,power_fault,macAddr"}, hostArgs(first, second)...)...)
	if code != 1 || !strings.Contains(out, "miners[2]{host,board,power_fault,macAddr,error}:\n  "+fmt.Sprintf("%q", first)+",\"601\",null,\"02:00:00:00:00:10\",null\n") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	code, out = execute(t, a, append([]string{"info", "--fields", "board,nonsense"}, hostArgs(first, second)...)...)
	if code != 2 || !strings.Contains(out, "code: unknown_field") || strings.Contains(out, "miners") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	code, out = execute(t, a, append([]string{"stats", "--fields", "power_w"}, hostArgs(first, second)...)...)
	if code != 1 || !strings.Contains(out, "miners[2]{host,power_w,error}:") {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestSeveralMinersJSON(t *testing.T) {
	first, _ := miner(t, renamed(t, "miner-a"))
	second, _ := statusMiner(t, http.StatusInternalServerError)
	a := New(func(string) string { return "" })
	code, out := execute(t, a, append([]string{"info", "--json", "--fields", "hostname"}, hostArgs(first, second)...)...)
	want := fmt.Sprintf("{\"count\":2,\"failed\":1,\"miners\":[{\"host\":%q,\"hostname\":\"miner-a\",\"error\":null},{\"host\":%q,\"hostname\":null,\"error\":\"miner_read_failed\"}],\"help\":[\"axeos-axi info --host '%s' for the error message of that miner\"]}\n", first, second, second)
	if code != 1 || out != want {
		t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
	}
}

func TestSeveralHostsOnOtherCommandsAreRefused(t *testing.T) {
	first, firstCalls := miner(t, fixture(t, "info"))
	second, secondCalls := miner(t, fixture(t, "info"))
	a := New(func(string) string { return "" })
	for _, command := range [][]string{
		{"scoreboard"}, {"stats", "--samples", "2"}, {"stats", "--columns", "power"}, {"logs"}, {"logs", "--lines", "3"}, {"health"},
		{"restart"}, {"restart", "--confirm"}, {"tuning", "--frequency", "550"}, {"pool", "--url", "pool.example.org"},
	} {
		code, out := execute(t, a, append(slices.Clone(command), hostArgs(first, second)...)...)
		if code != 2 || !strings.Contains(out, "code: usage") || !strings.Contains(out, "several miners are accepted by the home view, `info`, `asic`, `stats` and `firmware`") {
			t.Errorf("command=%v code=%d out=%s", command, code, out)
		}
	}
	for _, command := range [][]string{{"discover"}, {"skill", "install"}} {
		code, out := execute(t, a, append(slices.Clone(command), hostArgs(first, second)...)...)
		if code != 2 || !strings.Contains(out, "unknown flag --host") {
			t.Errorf("command=%v code=%d out=%s", command, code, out)
		}
	}
	if len(firstCalls()) != 0 || len(secondCalls()) != 0 {
		t.Fatalf("requests after a refused call: %v %v", firstCalls(), secondCalls())
	}
}

func TestSameHostTwiceAndInvalidHostSendNothing(t *testing.T) {
	first, firstCalls := miner(t, fixture(t, "info"))
	second, secondCalls := miner(t, fixture(t, "info"))
	a := New(func(string) string { return "" })
	for _, args := range [][]string{
		{"info", "--host", first, "--host", second, "--host", first},
		{"info", "--host", strings.TrimPrefix(first, "http://"), "--host", first},
		{"info", "--host", strings.ToUpper(first), "--host", first},
		{"info", "--host", first + "/#/", "--host", first},
		{"info", "--host", "192.0.2.10", "--host", "http://192.0.2.10/#/"},
		{"info", "--host", "192.0.2.10", "--host", "192.0.2.10:80"},
	} {
		code, out := execute(t, a, args...)
		if code != 2 || !strings.Contains(out, "code: usage") || !strings.Contains(out, "more than once") {
			t.Errorf("args=%v code=%d out=%s", args, code, out)
		}
	}
	code, out := execute(t, a, "info", "--host", first, "--host", "http://", "--host", second)
	if code != 2 || !strings.Contains(out, "code: invalid_host") {
		t.Errorf("code=%d out=%s", code, out)
	}
	if len(firstCalls()) != 0 || len(secondCalls()) != 0 {
		t.Fatalf("requests: %v %v", firstCalls(), secondCalls())
	}
}

func TestEnvironmentHostStaysOneHost(t *testing.T) {
	fromEnv, envCalls := miner(t, fixture(t, "info"))
	flagged, flagCalls := miner(t, fixture(t, "info"))
	a := New(func(string) string { return fromEnv })
	code, out := execute(t, a, "info", "--host", flagged)
	if code != 0 || strings.Contains(out, "miners") || len(envCalls()) != 0 || len(flagCalls()) != 1 {
		t.Fatalf("code=%d out=%s env=%v flag=%v", code, out, envCalls(), flagCalls())
	}
}
