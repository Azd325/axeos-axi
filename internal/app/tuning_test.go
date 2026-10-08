package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

const tuningReads = "GET /api/system/info,GET /api/system/asic"

func tuningMiner(t *testing.T, change func(info, asic map[string]any), answer http.HandlerFunc) (string, func() string) {
	t.Helper()
	info, asic := fixture(t, "info"), fixture(t, "asic")
	if change != nil {
		change(info, asic)
	}
	var mu sync.Mutex
	var requests []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		mu.Lock()
		requests = append(requests, strings.TrimSpace(r.Method+" "+r.URL.RequestURI()+" "+string(body)))
		mu.Unlock()
		var response map[string]any
		switch r.Method + " " + r.URL.RequestURI() {
		case "GET /api/system/info":
			response = info
		case "GET /api/system/asic":
			response = asic
		case "PATCH /api/system":
			if r.Header.Get("Content-Type") != "application/json" || r.ContentLength != int64(len(body)) {
				t.Errorf("content type=%q length=%d body=%q", r.Header.Get("Content-Type"), r.ContentLength, body)
			}
			answer(w, r)
			return
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(s.Close)
	return s.URL, func() string { mu.Lock(); defer mu.Unlock(); return strings.Join(requests, ",") }
}

func settingsSaved(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

func TestTuningWithoutConfirmSendsNoWrite(t *testing.T) {
	for _, tc := range []struct {
		name, body, rows, execute string
		args                      []string
	}{
		{"frequency", "  frequency: 550\n", "settings[1]{setting,present,new,changes}:\n  frequency_mhz,525,550,true\n", "--frequency 550", []string{"tuning", "--frequency", "550"}},
		{"core voltage", "  coreVoltage: 1150\n", "settings[1]{setting,present,new,changes}:\n  core_voltage_set_mv,1100,1150,true\n", "--core-voltage 1150", []string{"--core-voltage=1150", "tuning"}},
		{"both", "  frequency: 550\n  coreVoltage: 1150\n", "settings[2]{setting,present,new,changes}:\n  frequency_mhz,525,550,true\n  core_voltage_set_mv,1100,1150,true\n", "--frequency 550 --core-voltage 1150", []string{"tuning", "--core-voltage", "1150", "--frequency", "550"}},
		{"one of two changes", "  frequency: 525\n  coreVoltage: 1150\n", "settings[2]{setting,present,new,changes}:\n  frequency_mhz,525,525,false\n  core_voltage_set_mv,1100,1150,true\n", "--frequency 525 --core-voltage 1150", []string{"tuning", "--frequency", "525", "--core-voltage", "1150"}},
		{"no change", "  frequency: 525\n  coreVoltage: 1100\n", "settings[2]{setting,present,new,changes}:\n  frequency_mhz,525,525,false\n  core_voltage_set_mv,1100,1100,false\n", "--frequency 525 --core-voltage 1100", []string{"tuning", "--frequency", "525", "--core-voltage", "1100"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host, calls := tuningMiner(t, nil, settingsSaved)
			code, out := execute(t, New(func(string) string { return host }), tc.args...)
			want := "host: \"" + host + "\"\nrequest: PATCH /api/system\nbody:\n" + tc.body + "sent: false\n" + tc.rows +
				"effect: \"the values are active at once, without a restart, and the miner keeps them across a restart\"\n" +
				"execute: \"axeos-axi tuning --host '" + host + "' " + tc.execute + " --confirm\"\n"
			if code != 0 || out != want {
				t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
			}
			if calls() != tuningReads {
				t.Fatalf("requests=%s", calls())
			}
		})
	}
}

func TestTuningWithConfirmSendsOneWriteWithTheNamedSettings(t *testing.T) {
	for _, tc := range []struct {
		name, write, body, rows, revert string
		args                            []string
	}{
		{"frequency", `{"frequency":550}`, "  frequency: 550\n", "settings[1]{setting,present,new,changes}:\n  frequency_mhz,525,550,true\n", "--frequency 525", []string{"tuning", "--frequency", "550", "--confirm"}},
		{"core voltage", `{"coreVoltage":1150}`, "  coreVoltage: 1150\n", "settings[1]{setting,present,new,changes}:\n  core_voltage_set_mv,1100,1150,true\n", "--core-voltage 1100", []string{"--confirm", "tuning", "--core-voltage", "1150"}},
		{"both", `{"coreVoltage":1150,"frequency":550}`, "  frequency: 550\n  coreVoltage: 1150\n", "settings[2]{setting,present,new,changes}:\n  frequency_mhz,525,550,true\n  core_voltage_set_mv,1100,1150,true\n", "--frequency 525 --core-voltage 1100", []string{"tuning", "--frequency=550", "--core-voltage=1150", "--confirm"}},
		{"one of two changes", `{"coreVoltage":1100,"frequency":550}`, "  frequency: 550\n  coreVoltage: 1100\n", "settings[2]{setting,present,new,changes}:\n  frequency_mhz,525,550,true\n  core_voltage_set_mv,1100,1100,false\n", "--frequency 525 --core-voltage 1100", []string{"tuning", "--frequency", "550", "--core-voltage", "1100", "--confirm"}},
		{"no change", `{"coreVoltage":1100,"frequency":525}`, "  frequency: 525\n  coreVoltage: 1100\n", "settings[2]{setting,present,new,changes}:\n  frequency_mhz,525,525,false\n  core_voltage_set_mv,1100,1100,false\n", "--frequency 525 --core-voltage 1100", []string{"tuning", "--frequency", "525", "--core-voltage", "1100", "--confirm"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host, calls := tuningMiner(t, nil, settingsSaved)
			code, out := execute(t, New(func(string) string { return host }), tc.args...)
			want := "host: \"" + host + "\"\nrequest: PATCH /api/system\nbody:\n" + tc.body + "sent: true\n" + tc.rows +
				"result: \"the miner accepted the request; the values are active at once, without a restart, and the miner keeps them across a restart\"\n" +
				"help[2]: \"axeos-axi asic --host '" + host + "' shows frequency_mhz and core_voltage_set_mv\",\"axeos-axi tuning --host '" + host + "' " + tc.revert + " --confirm sets the previous values again\"\n"
			if code != 0 || out != want {
				t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
			}
			if calls() != tuningReads+",PATCH /api/system "+tc.write {
				t.Fatalf("requests=%s", calls())
			}
		})
	}
}

func TestTuningRefusalsSendNoWrite(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		change     func(info, asic map[string]any)
		args       []string
	}{
		{"frequency outside the list", "code: value_not_allowed\n  message: \"frequency 555 MHz is not in the list the miner reports; allowed values in MHz: 400, 490, 525, 550, 600, 625, 690\"\n", nil, []string{"--frequency", "555"}},
		{"core voltage outside the list", "code: value_not_allowed\n  message: \"core voltage 1300 mV is not in the list the miner reports; allowed values in mV: 1000, 1060, 1100, 1150, 1200, 1250\"\n", nil, []string{"--core-voltage", "1300"}},
		{"one value of two outside the list", "code: value_not_allowed\n  message: \"core voltage 9999 mV is not in the list the miner reports; ", nil, []string{"--frequency", "550", "--core-voltage", "9999"}},
		{"no frequency list", "code: no_allowed_values\n  message: the miner reports no list of allowed values for frequency; the write is refused\n", func(_, asic map[string]any) { delete(asic, "frequencyOptions") }, []string{"--frequency", "550"}},
		{"empty voltage list", "code: no_allowed_values\n  message: the miner reports no list of allowed values for core voltage; the write is refused\n", func(_, asic map[string]any) { asic["voltageOptions"] = []any{} }, []string{"--core-voltage", "1150"}},
		{"list that is not numbers", "code: no_allowed_values\n  message: the miner reports no list of allowed values for frequency; the write is refused\n", func(_, asic map[string]any) { asic["frequencyOptions"] = []any{float64(550), "600"} }, []string{"--frequency", "550"}},
		{"list of another setting is missing", "code: no_allowed_values\n  message: the miner reports no list of allowed values for core voltage; the write is refused\n", func(_, asic map[string]any) { delete(asic, "voltageOptions") }, []string{"--frequency", "550", "--core-voltage", "1150"}},
		{"no present value", "code: present_value_unknown\n  message: the miner reports no present value for frequency; the write is refused\n", func(info, _ map[string]any) { delete(info, "frequency") }, []string{"--frequency", "550"}},
		{"present value outside the list", "code: not_reversible\n  message: \"the present core voltage 1050 mV is not in the list the miner reports, so this tool cannot set it again; the write is refused\"\n", func(info, _ map[string]any) { info["coreVoltage"] = float64(1050) }, []string{"--core-voltage", "1150"}},
	} {
		for _, confirm := range []bool{false, true} {
			t.Run(tc.name, func(t *testing.T) {
				host, calls := tuningMiner(t, tc.change, settingsSaved)
				args := append([]string{"tuning"}, tc.args...)
				if confirm {
					args = append(args, "--confirm")
				}
				code, out := execute(t, New(func(string) string { return host }), args...)
				if code != 1 || !strings.HasPrefix(out, "error:\n  "+tc.want) || !strings.Contains(out, "\nhelp: \"axeos-axi ") {
					t.Fatalf("args=%v code=%d\n%s", args, code, out)
				}
				if calls() != tuningReads {
					t.Fatalf("requests=%s", calls())
				}
			})
		}
	}
}

func TestTuningLeavesAnUnnamedSettingUnchecked(t *testing.T) {
	host, calls := tuningMiner(t, func(info, asic map[string]any) {
		info["coreVoltage"] = float64(1050)
		delete(asic, "voltageOptions")
	}, settingsSaved)
	code, out := execute(t, New(func(string) string { return host }), "tuning", "--frequency", "550", "--confirm")
	if code != 0 || !strings.Contains(out, "sent: true\nsettings[1]{setting,present,new,changes}:\n  frequency_mhz,525,550,true\n") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if calls() != tuningReads+`,PATCH /api/system {"frequency":550}` {
		t.Fatalf("requests=%s", calls())
	}
}

func TestTuningReadFailuresSendNoWrite(t *testing.T) {
	for _, tc := range []struct {
		name, path, want, calls string
		status                  int
	}{
		{"info fails", "/api/system/info", "code: miner_read_failed\n  message: miner returned HTTP 500 for info\n", "GET /api/system/info", http.StatusInternalServerError},
		{"asic fails", "/api/system/asic", "code: miner_read_failed\n  message: miner returned HTTP 500 for asic\n", tuningReads, http.StatusInternalServerError},
		{"firmware without the asic path", "/api/system/asic", "code: not_supported\n  message: the list of allowed tuning values is not supported by this firmware\n", tuningReads, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var requests []string
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests = append(requests, r.Method+" "+r.URL.RequestURI())
				mu.Unlock()
				if r.URL.Path == tc.path {
					w.WriteHeader(tc.status)
					return
				}
				_ = json.NewEncoder(w).Encode(fixture(t, "info"))
			}))
			defer s.Close()
			code, out := execute(t, New(func(string) string { return s.URL }), "tuning", "--frequency", "550", "--confirm")
			if code != 1 || !strings.HasPrefix(out, "error:\n  "+tc.want) {
				t.Fatalf("code=%d\n%s", code, out)
			}
			mu.Lock()
			defer mu.Unlock()
			if strings.Join(requests, ",") != tc.calls {
				t.Fatalf("requests=%v", requests)
			}
		})
	}
}

func TestTuningWriteFailuresStateWhetherTheRequestWasSent(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		answer     http.HandlerFunc
	}{
		{"rejected by the firmware", "code: tuning_failed\n  message: the request was sent; miner returned HTTP 400 for settings; the miner did not confirm the change\n", restartAnswer(http.StatusBadRequest, "text/plain", "Wrong API input")},
		{"outside the allowed network", "code: tuning_failed\n  message: the request was sent; miner returned HTTP 401 for settings; the miner did not confirm the change\n", restartAnswer(http.StatusUnauthorized, "text/plain", "private-identifier")},
		{"redirect", "code: tuning_failed\n  message: the request was sent; miner returned HTTP 307 for settings; the miner did not confirm the change\n", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/api/system/info", http.StatusTemporaryRedirect)
		}},
		{"closed connection", "code: tuning_unconfirmed\n  message: \"the request was sent, but the miner closed the connection or did not answer in time; the change is unconfirmed\"\n", func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host, calls := tuningMiner(t, nil, tc.answer)
			code, out := execute(t, New(func(string) string { return host }), "tuning", "--frequency", "550", "--confirm")
			if code != 1 || !strings.HasPrefix(out, "error:\n  "+tc.want+"help: \"axeos-axi asic --host '"+host+"' shows frequency_mhz and core_voltage_set_mv") || strings.Contains(out, "private-identifier") {
				t.Fatalf("code=%d\n%s", code, out)
			}
			if calls() != tuningReads+`,PATCH /api/system {"frequency":550}` {
				t.Fatalf("requests=%s", calls())
			}
		})
	}
}

func TestTuningRejectsInputBeforeNetwork(t *testing.T) {
	host, calls := tuningMiner(t, nil, settingsSaved)
	a := New(func(string) string { return host })
	for _, args := range [][]string{
		{"tuning"}, {"tuning", "--confirm"}, {"--confirm", "tuning"},
		{"tuning", "--frequency"}, {"tuning", "--frequency", "--confirm"}, {"tuning", "--frequency="}, {"tuning", "--frequency", "fast"},
		{"tuning", "--frequency", "0"}, {"tuning", "--frequency", "-550"}, {"tuning", "--frequency", "550.5"}, {"tuning", "--frequency", "550MHz"},
		{"tuning", "--core-voltage", "1.15"}, {"tuning", "--core-voltage", "1150mV"}, {"tuning", "--core-voltage"},
		{"tuning", "--frequency", "Default"}, {"tuning", "--core-voltage", "defaults"}, {"tuning", "--frequency", "default", "--frequency", "550"},
		{"tuning", "--frequency", "550", "--frequency", "600"}, {"tuning", "--frequency=550", "--frequency=550", "--confirm"},
		{"tuning", "--core-voltage", "1150", "--core-voltage", "1200", "--confirm"},
		{"tuning", "--frequency", "550", "--fields", "host"}, {"tuning", "--frequency", "550", "--lines", "1"}, {"tuning", "--frequency", "550", "--timeout", "1"},
		{"tuning", "--frequency", "550", "--voltage", "1150"}, {"tuning", "--frequency", "550", "--fan", "50"}, {"tuning", "--frequency", "550", "--confirm=true"},
		{"tuning", "--frequency", "550", "550"}, {"tuning", "--frequency", "550", "--confirm", "--help", "--bad"},
		{"--frequency", "550"}, {"asic", "--frequency", "550"}, {"restart", "--core-voltage", "1150"}, {"restart", "--frequency", "550", "--confirm"},
	} {
		code, out := execute(t, a, args...)
		if code != 2 || !strings.Contains(out, "code: usage") || !strings.Contains(out, "valid flags:") {
			t.Errorf("args=%v code=%d out=%s", args, code, out)
		}
	}
	for want, args := range map[string][]string{
		"tuning requires --frequency <MHz>, --core-voltage <mV> or both":            {"tuning", "--confirm"},
		"--frequency was given more than once":                                      {"tuning", "--frequency", "550", "--frequency", "600"},
		"--core-voltage requires a whole number from 1, or default":                 {"tuning", "--core-voltage", "1.15"},
		"--frequency requires a whole number from 1, or default":                    {"tuning", "--frequency", "Default"},
		"unknown flag --fields for `tuning`; it prints a fixed result":              {"tuning", "--frequency", "550", "--fields", "host"},
		"unknown flag --core-voltage; it is a flag of `tuning` only":                {"restart", "--core-voltage", "1150"},
		"valid flags: --host, --frequency, --core-voltage, --confirm, --json, --he": {"tuning"},
	} {
		if code, out := execute(t, a, args...); code != 2 || !strings.Contains(out, want) {
			t.Errorf("args=%v code=%d out=%s", args, code, out)
		}
	}
	code, out := execute(t, New(func(string) string { return "" }), "tuning", "--frequency", "550", "--confirm")
	if code != 2 || !strings.Contains(out, "code: host_required") {
		t.Fatalf("%d %s", code, out)
	}
	code, out = execute(t, a, "tuning", "--frequency", "550", "--confirm", "--host", "http://192.0.2.10/path")
	if code != 2 || !strings.Contains(out, "code: invalid_host") {
		t.Fatalf("%d %s", code, out)
	}
	if calls() != "" {
		t.Fatalf("requests before usage validation: %s", calls())
	}
}

func TestTuningUnreachableMiner(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	host := s.URL
	s.Close()
	code, out := execute(t, New(func(string) string { return host }), "tuning", "--frequency", "550", "--confirm")
	if code != 1 || !strings.HasPrefix(out, "error:\n  code: miner_read_failed\n") || strings.Contains(out, host) {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestTuningHelpAndVersionSendNothing(t *testing.T) {
	a := New(func(string) string { t.Fatal("offline command read environment"); return "" })
	a.Version = "1.2.3"
	for _, args := range [][]string{{"tuning", "--help"}, {"tuning", "--frequency", "550", "--confirm", "--help"}, {"--help", "tuning"}} {
		code, out := execute(t, a, args...)
		if code != 0 || !strings.HasPrefix(out, "command: tuning\ndescription: \"Changes the miner: ") || !strings.Contains(out, "frequency: \"--frequency <MHz|default>; ") || !strings.Contains(out, "core_voltage: \"--core-voltage <mV|default>; ") || !strings.Contains(out, "examples[4]:") || strings.Contains(out, "--fields") {
			t.Fatalf("args=%v code=%d\n%s", args, code, out)
		}
	}
	if code, out := execute(t, a, "tuning", "--version"); code != 0 || out != "1.2.3\n" {
		t.Fatalf("%d %s", code, out)
	}
}

func TestHomeNamesTuning(t *testing.T) {
	host, calls := miner(t, fixture(t, "info"))
	code, out := execute(t, New(func(string) string { return host }))
	if code != 0 || !strings.Contains(out, "\"axeos-axi tuning --host '"+host+"' --frequency <MHz> --core-voltage <mV> for a preview of a tuning change; it sends no write request without --confirm\"") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if !slices.Equal(calls(), []string{"GET /api/system/info"}) {
		t.Fatalf("requests=%v", calls())
	}
}
