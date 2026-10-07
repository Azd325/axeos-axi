package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

func statsMiner(t *testing.T, info, stats map[string]any) (string, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var requests []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		body := map[string]any{"/api/system/info": info, "/api/system/statistics": stats}[r.URL.Path]
		if body == nil {
			t.Errorf("unexpected request: %s", r.URL.RequestURI())
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(s.Close)
	return s.URL, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), requests...) }
}

const defaultStatsOutput = `sample_count: 3
logging_interval_s: 120
current_timestamp_ms: 612447946
latest_timestamp_ms: 612447553
hashrate_ghs: 979.0009766
hashrate_1h_ghs: 1070.2735596
chip_temperature_c: 64.875
regulator_temperature_c: 73
power_w: 20.1245117
`

func TestStatsWithoutHistoryFlagsIsUnchanged(t *testing.T) {
	host, calls := statsMiner(t, fixture(t, "info"), fixture(t, "statistics"))
	code, out := execute(t, New(func(string) string { return host }), "stats")
	if code != 0 || out != defaultStatsOutput {
		t.Fatalf("code=%d output=%q", code, out)
	}
	if want := []string{"GET /api/system/info", "GET /api/system/statistics"}; !slices.Equal(calls(), want) {
		t.Fatalf("requests=%v", calls())
	}
	code, out = execute(t, New(func(string) string { return host }), "stats", "--fields", "sample_count,power_w")
	if code != 0 || out != "sample_count: 3\npower_w: 20.1245117\n" {
		t.Fatalf("code=%d output=%q", code, out)
	}
}

func TestStatsColumnsSendsTheQueryAndPrintsTheNewestSample(t *testing.T) {
	host, calls := statsMiner(t, fixture(t, "info"), fixture(t, "statistics_columns"))
	code, out := execute(t, New(func(string) string { return host }), "stats", "--columns", "fanRpm,power")
	want := `sample_count: 3
shown_samples: 1
logging_interval_s: 120
current_timestamp_ms: 612447946
samples[1]{timestamp,fanRpm,power}:
  612447553,4081,20.1245117
help[1]: "axeos-axi stats --host '` + host + `' --samples all --columns fanRpm,power for all 3 samples"
`
	if code != 0 || out != want {
		t.Fatalf("code=%d output=%q", code, out)
	}
	if got := calls(); !slices.Equal(got, []string{"GET /api/system/info", "GET /api/system/statistics?columns=fanRpm,power"}) {
		t.Fatalf("requests=%v", got)
	}
}

func TestStatsSamplesUseTheDefaultRequestAndColumns(t *testing.T) {
	for _, tc := range []struct{ samples, want string }{
		{"2", "shown_samples: 2\n"},
		{"all", "shown_samples: 3\n"},
		{"9", "shown_samples: 3\n"},
	} {
		host, calls := statsMiner(t, fixture(t, "info"), fixture(t, "statistics"))
		code, out := execute(t, New(func(string) string { return host }), "stats", "--samples", tc.samples)
		if code != 0 || !strings.Contains(out, "sample_count: 3\n"+tc.want) || !strings.Contains(out, "samples["+strings.TrimPrefix(strings.TrimSuffix(tc.want, "\n"), "shown_samples: ")+"]{timestamp,hashrate,hashrate_1h,asicTemp,vrTemp,power}:") {
			t.Fatalf("--samples %s: code=%d output=%q", tc.samples, code, out)
		}
		if got := calls(); !slices.Equal(got, []string{"GET /api/system/info", "GET /api/system/statistics"}) {
			t.Fatalf("requests=%v", got)
		}
		if cut := tc.samples == "2"; strings.Contains(out, "help[1]:") != cut {
			t.Fatalf("--samples %s: help line must appear only when the output is cut: %q", tc.samples, out)
		}
	}
	host, _ := statsMiner(t, fixture(t, "info"), fixture(t, "statistics"))
	_, out := execute(t, New(func(string) string { return host }), "stats", "--samples", "2")
	if !strings.Contains(out, "  612446553,979.0009766,1070.2729492,64.75,72,20.1245117\n  612447553,979.0009766,1070.2735596,64.875,73,20.1245117\n") {
		t.Fatalf("rows must be the newest two, oldest first: %q", out)
	}
	if !strings.Contains(out, "axeos-axi stats --host '"+host+"' --samples all for all 3 samples") {
		t.Fatalf("help: %q", out)
	}
}

func TestStatsColumnsPrintNullForNamesTheOlderFirmwareLabelsDifferently(t *testing.T) {
	older := map[string]any{
		"currentTimestamp": float64(612447946),
		"labels":           []any{"hashRate", "temp", "vrTemp", "power", "voltage", "current", "coreVoltageActual", "fanspeed", "fanrpm", "wifiRSSI", "freeHeap", "timestamp"},
		"statistics": []any{
			[]any{float64(1026.9), float64(64.6), float64(73), float64(20.1757813), float64(4976.5), float64(13875), float64(1093), float64(60), float64(4069), float64(-55), float64(150000), float64(612409553)},
			[]any{float64(1027.9), float64(64.7), float64(73), float64(20.2), float64(4976.5), float64(13880), float64(1093), float64(60), float64(4070), float64(-55), float64(150000), float64(612429553)},
		},
	}
	host, _ := statsMiner(t, fixture(t, "info"), older)
	code, out := execute(t, New(func(string) string { return host }), "stats", "--columns", "power,fanRpm", "--samples", "all")
	if code != 0 || !strings.Contains(out, "samples[2]{timestamp,power,fanRpm}:\n  612409553,20.1757813,null\n  612429553,20.2,null\n") || strings.Contains(out, "hashrate") || strings.Contains(out, "help") {
		t.Fatalf("code=%d output=%q", code, out)
	}
}

func TestStatsColumnTheMinerOmitsPrintsNull(t *testing.T) {
	host, _ := statsMiner(t, fixture(t, "info"), fixture(t, "statistics_columns"))
	code, out := execute(t, New(func(string) string { return host }), "stats", "--columns", "fan2Rpm")
	if code != 0 || !strings.Contains(out, "samples[1]{timestamp,fan2Rpm}:\n  612447553,null\n") {
		t.Fatalf("code=%d output=%q", code, out)
	}
}

func TestStatsHistoryEmptyStates(t *testing.T) {
	disabled := fixture(t, "info")
	disabled["statsFrequency"] = float64(0)
	none := fixture(t, "statistics")
	none["statistics"] = []any{}
	for name, tc := range map[string]struct {
		info, stats map[string]any
		want        string
	}{
		"disabled":   {disabled, fixture(t, "statistics"), "state: statistics logging disabled on miner (statsFrequency=0); 0 recorded samples available\nsample_count: 0\nlogging_interval_s: 0\n"},
		"no samples": {fixture(t, "info"), none, "state: 0 recorded statistics samples found on miner\nsample_count: 0\nlogging_interval_s: 120\n"},
	} {
		host, _ := statsMiner(t, tc.info, tc.stats)
		code, out := execute(t, New(func(string) string { return host }), "stats", "--columns", "power")
		if code != 0 || out != tc.want {
			t.Errorf("%s: code=%d output=%q", name, code, out)
		}
	}
}

func TestStatsHistoryFlagErrorsSendNoRequest(t *testing.T) {
	host, calls := statsMiner(t, fixture(t, "info"), fixture(t, "statistics"))
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"stats", "--columns", "bogus"}, "--columns requires unique comma-separated names from: hashrate,hashrate_1m,"},
		{[]string{"stats", "--columns", "power,power"}, "--columns requires unique"},
		{[]string{"stats", "--columns", "timestamp"}, "--columns requires unique"},
		{[]string{"stats", "--columns", "power,"}, "--columns requires unique"},
		{[]string{"stats", "--samples", "0"}, "--samples requires a whole number from 1, or all"},
		{[]string{"stats", "--samples", "x"}, "--samples requires a whole number from 1, or all"},
		{[]string{"stats", "--samples", "2", "--samples", "3"}, "--samples was given more than once"},
		{[]string{"stats", "--samples", "2", "--fields", "power_w"}, "--fields cannot be combined"},
		{[]string{"stats", "--columns", "power", "--fields", "power_w"}, "--fields cannot be combined"},
		{[]string{"info", "--samples", "2"}, "unknown flag --samples; it is a flag of `stats` only"},
		{[]string{"logs", "--columns", "power"}, "unknown flag --columns; it is a flag of `stats` only"},
	} {
		code, out := execute(t, New(func(string) string { return host }), append(tc.args, "--host", host)...)
		if code != 2 || !strings.Contains(out, tc.want) || !strings.Contains(out, "--samples, --columns") && tc.args[0] == "stats" {
			t.Errorf("%v: code=%d output=%q", tc.args, code, out)
		}
	}
	if got := calls(); len(got) != 0 {
		t.Fatalf("requests=%v", got)
	}
}

func TestStatsHelpNamesTheHistoryFlags(t *testing.T) {
	code, out := execute(t, New(func(string) string { return "" }), "stats", "--help")
	for _, want := range []string{"--samples <n|all>", "--columns <name,...>", "without --samples prints the newest sample", "firmware older than v2.11.0 ignores the query", "fan2Rpm", "--samples 10 --columns fanRpm,wifiRssi"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("stats help lacks %q", want)
		}
	}
	_, out = execute(t, New(func(string) string { return "" }), "logs", "--help")
	if strings.Contains(out, "--samples") {
		t.Error("logs help names --samples")
	}
}
