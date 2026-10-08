package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	tuningEffects    = "\"the values are active at once, without a restart, and the miner keeps them across a restart\"\n"
	tuningAccepted   = "result: \"each miner accepted the request; the values are active at once, without a restart, and the miner keeps them across a restart\"\n"
	tuningCheckWrite = "result: \"%d of %d miners failed the check before the write, so no write request was sent to any miner\"\n"
)

func tuningVerify(hosts ...string) string {
	return "\"axeos-axi asic " + hostList(hosts...) + " shows frequency_mhz and core_voltage_set_mv of each miner\""
}

func tuningRestore(host, arguments string) string {
	return "\"axeos-axi tuning --host '" + host + "' " + arguments + " --confirm\""
}

func frequencyAt(mhz float64) func(info, asic map[string]any) {
	return func(info, _ map[string]any) { info["frequency"] = mhz }
}

func TestTuningOfSeveralMinersWithTheSameList(t *testing.T) {
	first, firstCalls := tuningMiner(t, nil, settingsSaved)
	second, secondCalls := tuningMiner(t, nil, settingsSaved)
	a := New(noHost)

	code, out := execute(t, a, onHosts([]string{"tuning", "--frequency", "550", "--core-voltage", "1150"}, first, second)...)
	want := severalPoolHead + "sent: false\ncount: 2\nfailed: 0\n" +
		fmt.Sprintf("miners[2]{host,frequency_mhz_present,frequency_mhz_new,core_voltage_set_mv_present,core_voltage_set_mv_new,changes,error}:\n  %q,525,550,1100,1150,true,null\n  %q,525,550,1100,1150,true,null\n", first, second) +
		"effect: " + tuningEffects +
		"execute: \"axeos-axi tuning " + hostList(first, second) + " --frequency 550 --core-voltage 1150 --confirm\"\n"
	if code != 0 || out != want {
		t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
	}
	if firstCalls() != tuningReads || secondCalls() != tuningReads {
		t.Fatalf("requests: %s | %s", firstCalls(), secondCalls())
	}

	code, out = execute(t, a, onHosts([]string{"tuning", "--frequency", "550", "--confirm"}, second, first)...)
	want = severalPoolHead + "sent: true\ncount: 2\nfailed: 0\nchanged: 2\n" +
		"miners[2]{host,frequency_mhz_present,frequency_mhz_new,result,restore,error}:\n" +
		fmt.Sprintf("  %q,525,550,changed,%s,null\n  %q,525,550,changed,%s,null\n", second, tuningRestore(second, "--frequency 525"), first, tuningRestore(first, "--frequency 525")) +
		tuningAccepted +
		"help[2]: " + tuningVerify(second, first) + "," + restoreHelp + "\n"
	if code != 0 || out != want {
		t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
	}
	write := `,PATCH /api/system {"frequency":550}`
	if firstCalls() != tuningReads+","+tuningReads+write || secondCalls() != tuningReads+","+tuningReads+write {
		t.Fatalf("requests: %s | %s", firstCalls(), secondCalls())
	}
}

func TestTuningOfSeveralMinersSendsNoWriteWhenOneListLacksTheValue(t *testing.T) {
	shortList := func(_, asic map[string]any) {
		asic["frequencyOptions"] = []any{float64(400), float64(490), float64(525)}
	}
	for _, confirm := range []bool{false, true} {
		t.Run(fmt.Sprintf("confirm %v", confirm), func(t *testing.T) {
			first, firstCalls := tuningMiner(t, nil, settingsSaved)
			second, secondCalls := tuningMiner(t, shortList, settingsSaved)
			third, thirdCalls := tuningMiner(t, nil, settingsSaved)
			args := onHosts([]string{"tuning", "--frequency", "550"}, first, second, third)
			inspect := "\"axeos-axi tuning --host '" + second + "' --frequency 550 for the error message of that miner\""
			want := severalPoolHead + "sent: false\ncount: 3\nfailed: 1\n"
			if confirm {
				args = append(args, "--confirm")
				want += "changed: 0\n" +
					"miners[3]{host,frequency_mhz_present,frequency_mhz_new,result,restore,allowed,error}:\n" +
					fmt.Sprintf("  %q,525,550,not_attempted,null,null,null\n  %q,null,null,not_attempted,null,\"frequency_mhz: 400, 490, 525\",value_not_allowed\n  %q,525,550,not_attempted,null,null,null\n", first, second, third) +
					fmt.Sprintf(tuningCheckWrite, 1, 3) +
					"help[1]: " + inspect + "\n"
			} else {
				want += "miners[3]{host,frequency_mhz_present,frequency_mhz_new,changes,allowed,error}:\n" +
					fmt.Sprintf("  %q,525,550,true,null,null\n  %q,null,null,null,\"frequency_mhz: 400, 490, 525\",value_not_allowed\n  %q,525,550,true,null,null\n", first, second, third) +
					"effect: " + tuningEffects +
					"execute: \"axeos-axi tuning " + hostList(first, second, third) + " --frequency 550 --confirm\"\n" +
					"help[2]: " + inspect + ",the execute command sends no write request while a miner fails the check\n"
			}
			code, out := execute(t, New(noHost), args...)
			if code != 1 || out != want {
				t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
			}
			if firstCalls() != tuningReads || secondCalls() != tuningReads || thirdCalls() != tuningReads {
				t.Fatalf("requests: %s | %s | %s", firstCalls(), secondCalls(), thirdCalls())
			}
		})
	}
}

func TestTuningDefaultIsTheDefaultOfEachMiner(t *testing.T) {
	first, firstCalls := tuningMiner(t, frequencyAt(550), settingsSaved)
	second, secondCalls := tuningMiner(t, func(_, asic map[string]any) {
		asic["defaultFrequency"] = float64(490)
		asic["defaultVoltage"] = float64(1200)
	}, settingsSaved)
	a := New(noHost)

	code, out := execute(t, a, onHosts([]string{"tuning", "--frequency", "default", "--core-voltage=default"}, first, second)...)
	want := severalPoolHead + "sent: false\ncount: 2\nfailed: 0\n" +
		fmt.Sprintf("miners[2]{host,frequency_mhz_present,frequency_mhz_new,core_voltage_set_mv_present,core_voltage_set_mv_new,changes,error}:\n  %q,550,525,1100,1150,true,null\n  %q,525,490,1100,1200,true,null\n", first, second) +
		"effect: " + tuningEffects +
		"execute: \"axeos-axi tuning " + hostList(first, second) + " --frequency default --core-voltage default --confirm\"\n"
	if code != 0 || out != want {
		t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
	}

	code, out = execute(t, a, onHosts([]string{"tuning", "--frequency", "default", "--core-voltage", "default", "--confirm"}, first, second)...)
	want = severalPoolHead + "sent: true\ncount: 2\nfailed: 0\nchanged: 2\n" +
		"miners[2]{host,frequency_mhz_present,frequency_mhz_new,core_voltage_set_mv_present,core_voltage_set_mv_new,result,restore,error}:\n" +
		fmt.Sprintf("  %q,550,525,1100,1150,changed,%s,null\n  %q,525,490,1100,1200,changed,%s,null\n", first, tuningRestore(first, "--frequency 550 --core-voltage 1100"), second, tuningRestore(second, "--frequency 525 --core-voltage 1100")) +
		tuningAccepted +
		"help[2]: " + tuningVerify(first, second) + "," + restoreHelp + "\n"
	if code != 0 || out != want {
		t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
	}
	if firstCalls() != tuningReads+","+tuningReads+`,PATCH /api/system {"coreVoltage":1150,"frequency":525}` || secondCalls() != tuningReads+","+tuningReads+`,PATCH /api/system {"coreVoltage":1200,"frequency":490}` {
		t.Fatalf("requests: %s | %s", firstCalls(), secondCalls())
	}
}

func TestTuningDefaultWithOneHostPrintsTheNumber(t *testing.T) {
	host, calls := tuningMiner(t, frequencyAt(550), settingsSaved)
	a := New(func(string) string { return host })

	code, out := execute(t, a, "tuning", "--frequency", "default")
	want := "host: \"" + host + "\"\nrequest: PATCH /api/system\nbody:\n  frequency: 525\nsent: false\n" +
		"settings[1]{setting,present,new,changes}:\n  frequency_mhz,550,525,true\n" +
		"effect: " + tuningEffects +
		"execute: \"axeos-axi tuning --host '" + host + "' --frequency default --confirm\"\n"
	if code != 0 || out != want {
		t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
	}

	code, out = execute(t, a, "tuning", "--core-voltage", "default", "--confirm")
	want = "host: \"" + host + "\"\nrequest: PATCH /api/system\nbody:\n  coreVoltage: 1150\nsent: true\n" +
		"settings[1]{setting,present,new,changes}:\n  core_voltage_set_mv,1100,1150,true\n" +
		"result: \"the miner accepted the request; the values are active at once, without a restart, and the miner keeps them across a restart\"\n" +
		"help[2]: \"axeos-axi asic --host '" + host + "' shows frequency_mhz and core_voltage_set_mv\",\"axeos-axi tuning --host '" + host + "' --core-voltage 1100 --confirm sets the previous values again\"\n"
	if code != 0 || out != want {
		t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
	}
	if calls() != tuningReads+","+tuningReads+`,PATCH /api/system {"coreVoltage":1150}` {
		t.Fatalf("requests=%s", calls())
	}
}

func TestTuningDefaultOfAMinerWithoutADefaultSendsNoWrite(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(info, asic map[string]any)
	}{
		{"no default", func(_, asic map[string]any) { delete(asic, "defaultFrequency") }},
		{"a default that is not a number", func(_, asic map[string]any) { asic["defaultFrequency"] = "525" }},
		{"a default of zero", func(_, asic map[string]any) { asic["defaultFrequency"] = float64(0) }},
		{"a default that is not a whole number", func(_, asic map[string]any) { asic["defaultFrequency"] = 525.5 }},
	} {
		for _, confirm := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, confirm %v", tc.name, confirm), func(t *testing.T) {
				first, firstCalls := tuningMiner(t, frequencyAt(550), settingsSaved)
				second, secondCalls := tuningMiner(t, tc.change, settingsSaved)
				args := onHosts([]string{"tuning", "--frequency", "default"}, first, second)
				rows := fmt.Sprintf("miners[2]{host,frequency_mhz_present,frequency_mhz_new,changes,error}:\n  %q,550,525,true,null\n  %q,null,null,null,no_default_value\n", first, second)
				if confirm {
					args = append(args, "--confirm")
					rows = "changed: 0\n" + fmt.Sprintf("miners[2]{host,frequency_mhz_present,frequency_mhz_new,result,restore,error}:\n  %q,550,525,not_attempted,null,null\n  %q,null,null,not_attempted,null,no_default_value\n", first, second) +
						fmt.Sprintf(tuningCheckWrite, 1, 2)
				}
				code, out := execute(t, New(noHost), args...)
				if code != 1 || !strings.HasPrefix(out, severalPoolHead+"sent: false\ncount: 2\nfailed: 1\n"+rows) {
					t.Fatalf("code=%d\n%s\nwant rows\n%s", code, out, rows)
				}
				if firstCalls() != tuningReads || secondCalls() != tuningReads {
					t.Fatalf("requests: %s | %s", firstCalls(), secondCalls())
				}

				code, out = execute(t, New(func(string) string { return second }), args[:3]...)
				wantError := "error:\n  code: no_default_value\n  message: the miner reports no default value for frequency; the write is refused\n" +
					"help: \"axeos-axi asic --host '" + second + "' --fields defaultFrequency,frequencyOptions shows the default and the list the miner reports\"\n"
				if code != 1 || out != wantError {
					t.Fatalf("code=%d\n%s\nwant\n%s", code, out, wantError)
				}
			})
		}
	}
}

func TestTuningDefaultOutsideTheListIsRefused(t *testing.T) {
	host, calls := tuningMiner(t, func(_, asic map[string]any) { asic["defaultVoltage"] = float64(1166) }, settingsSaved)
	code, out := execute(t, New(func(string) string { return host }), "tuning", "--core-voltage", "default", "--confirm")
	if code != 1 || !strings.HasPrefix(out, "error:\n  code: value_not_allowed\n  message: \"core voltage 1166 mV is not in the list the miner reports; allowed values in mV: 1000, 1060, 1100, 1150, 1200, 1250\"\n") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if calls() != tuningReads {
		t.Fatalf("requests=%s", calls())
	}
}

func TestTuningOfSeveralMinersStopsAtTheFirstFailedWrite(t *testing.T) {
	first, firstCalls := tuningMiner(t, nil, settingsSaved)
	second, secondCalls := tuningMiner(t, nil, restartAnswer(http.StatusInternalServerError, "text/plain", "private-identifier"))
	third, thirdCalls := tuningMiner(t, nil, settingsSaved)
	code, out := execute(t, New(noHost), onHosts([]string{"tuning", "--core-voltage", "1150", "--confirm"}, first, second, third)...)
	want := severalPoolHead + "sent: true\ncount: 3\nfailed: 1\nchanged: 1\n" +
		"miners[3]{host,core_voltage_set_mv_present,core_voltage_set_mv_new,result,restore,error}:\n" +
		fmt.Sprintf("  %q,1100,1150,changed,%s,null\n  %q,1100,1150,failed,%s,tuning_failed\n  %q,1100,1150,not_attempted,null,null\n", first, tuningRestore(first, "--core-voltage 1100"), second, tuningRestore(second, "--core-voltage 1100"), third) +
		"result: \"the write to miner 2 of 3 failed: the request was sent; miner returned HTTP 500 for settings; the call stopped with 1 changed before it and 1 not attempted after it\"\n" +
		"help[2]: \"axeos-axi asic " + hostList(first, second, third) + " shows frequency_mhz and core_voltage_set_mv of each miner; read them before another tuning call\"," + restoreHelp + "\n"
	if code != 1 || out != want {
		t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
	}
	write := `,PATCH /api/system {"coreVoltage":1150}`
	if firstCalls() != tuningReads+write || secondCalls() != tuningReads+write || thirdCalls() != tuningReads {
		t.Fatalf("requests: %s | %s | %s", firstCalls(), secondCalls(), thirdCalls())
	}
}

func TestTuningOfSeveralMinersSendsARequestToAMinerAtTheNewValue(t *testing.T) {
	first, firstCalls := tuningMiner(t, frequencyAt(550), settingsSaved)
	second, secondCalls := tuningMiner(t, nil, settingsSaved)
	a := New(noHost)

	code, out := execute(t, a, onHosts([]string{"tuning", "--frequency", "550"}, first, second)...)
	if code != 0 || !strings.Contains(out, fmt.Sprintf("miners[2]{host,frequency_mhz_present,frequency_mhz_new,changes,error}:\n  %q,550,550,false,null\n  %q,525,550,true,null\n", first, second)) {
		t.Fatalf("code=%d\n%s", code, out)
	}

	code, out = execute(t, a, onHosts([]string{"tuning", "--frequency", "550", "--confirm"}, first, second)...)
	want := severalPoolHead + "sent: true\ncount: 2\nfailed: 0\nchanged: 2\n" +
		"miners[2]{host,frequency_mhz_present,frequency_mhz_new,result,restore,error}:\n" +
		fmt.Sprintf("  %q,550,550,changed,%s,null\n  %q,525,550,changed,%s,null\n", first, tuningRestore(first, "--frequency 550"), second, tuningRestore(second, "--frequency 525")) +
		tuningAccepted +
		"help[2]: " + tuningVerify(first, second) + "," + restoreHelp + "\n"
	if code != 0 || out != want {
		t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
	}
	write := `,PATCH /api/system {"frequency":550}`
	if firstCalls() != tuningReads+","+tuningReads+write || secondCalls() != tuningReads+","+tuningReads+write {
		t.Fatalf("requests: %s | %s", firstCalls(), secondCalls())
	}
}

func TestTuningOfSeveralMinersSendsNoWriteWhenOneMinerFailsARead(t *testing.T) {
	noAsicPath := func(t *testing.T) string {
		info := fixture(t, "info")
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/api/system/info" {
				http.NotFound(w, r)
				return
			}
			if err := json.NewEncoder(w).Encode(info); err != nil {
				t.Error(err)
			}
		}))
		t.Cleanup(s.Close)
		return s.URL
	}
	for _, tc := range []struct {
		name, code string
		broken     func(t *testing.T) string
	}{
		{"a miner that does not answer", "miner_read_failed", closedPort},
		{"a miner without the asic path", "not_supported", noAsicPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, firstCalls := tuningMiner(t, nil, settingsSaved)
			broken := tc.broken(t)
			code, out := execute(t, New(noHost), onHosts([]string{"tuning", "--frequency", "550", "--confirm"}, first, broken)...)
			want := severalPoolHead + "sent: false\ncount: 2\nfailed: 1\nchanged: 0\n" +
				"miners[2]{host,frequency_mhz_present,frequency_mhz_new,result,restore,error}:\n" +
				fmt.Sprintf("  %q,525,550,not_attempted,null,null\n  %q,null,null,not_attempted,null,%s\n", first, broken, tc.code) +
				fmt.Sprintf(tuningCheckWrite, 1, 2) +
				"help[1]: \"axeos-axi tuning --host '" + broken + "' --frequency 550 for the error message of that miner\"\n"
			if code != 1 || out != want {
				t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
			}
			if firstCalls() != tuningReads {
				t.Fatalf("requests: %s", firstCalls())
			}
		})
	}
}

func TestTuningWithTheSameHostTwiceSendsNothing(t *testing.T) {
	host, calls := tuningMiner(t, nil, settingsSaved)
	code, out := execute(t, New(noHost), onHosts([]string{"tuning", "--frequency", "550", "--confirm"}, host, host)...)
	if code != 2 || !strings.Contains(out, "code: usage") || !strings.Contains(out, "more than once") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if calls() != "" {
		t.Fatalf("requests=%s", calls())
	}
}
