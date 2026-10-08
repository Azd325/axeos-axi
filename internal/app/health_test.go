package app

import (
	"strings"
	"testing"
)

func healthInfo(t *testing.T, changes map[string]any) map[string]any {
	t.Helper()
	info := fixture(t, "info")
	for k, v := range changes {
		if v == deleted {
			delete(info, k)
			continue
		}
		info[k] = v
	}
	return info
}

const deleted = "\x00deleted"

func ruleRow(t *testing.T, out, rule string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), rule+",") {
			return strings.TrimSpace(line)
		}
	}
	t.Fatalf("no row for %s in\n%s", rule, out)
	return ""
}

func TestHealthRules(t *testing.T) {
	for _, tc := range []struct {
		name    string
		changes map[string]any
		verdict string
		exit    int
		rule    string
		result  string
		reason  string
	}{
		{"all ok", nil, "healthy", 0, "hashrate", "ok", ""},
		{"power fault", map[string]any{"power_fault": "VIN low"}, "unhealthy", 3, "power_fault", "failed", "the miner reports a power fault"},
		{"hardware fault", map[string]any{"hardware_fault": "Fan Control Failed (Auto)"}, "unhealthy", 3, "hardware_fault", "failed", "the miner reports a hardware fault"},
		{"overheat mode", map[string]any{"overheat_mode": 1}, "unhealthy", 3, "overheat_mode", "failed", "overheat mode is active"},
		{"mining paused", map[string]any{"miningPaused": true}, "unhealthy", 3, "mining_paused", "failed", "mining is paused"},
		{"fallback pool", map[string]any{"isUsingFallbackStratum": 1}, "unhealthy", 3, "fallback_pool", "failed", "the miner runs on the fallback pool"},
		{"low hashrate", map[string]any{"hashRate_1h": 856.0}, "unhealthy", 3, "hashrate", "failed", "1h hashrate is 80% of the expected hashrate"},
		{"hashrate above the limit", map[string]any{"hashRate_1h": 857.0}, "healthy", 0, "hashrate", "ok", ""},
		{"many rejected shares", map[string]any{"sharesAccepted": 94, "sharesRejected": 6}, "unhealthy", 3, "rejected_shares", "failed", "6.0% of all shares are rejected"},
		{"rejected shares at the limit", map[string]any{"sharesAccepted": 95, "sharesRejected": 5}, "healthy", 0, "rejected_shares", "ok", ""},
		{"fan stopped while hashing", map[string]any{"fanrpm": 0}, "unhealthy", 3, "fan", "failed", "a fan reads 0 rpm while the miner hashes at 1039.52 GH/s"},
		{"fan stopped without hashing", map[string]any{"fanrpm": 0, "hashRate": 0}, "healthy", 0, "fan", "ok", ""},
		{"single fan board ignores fan2rpm 0", nil, "healthy", 0, "fan", "ok", ""},
		{"second fan stopped", map[string]any{"boardVersion": "302", "fan2rpm": 0}, "unhealthy", 3, "fan", "failed", "a fan reads 0 rpm while the miner hashes at 1039.52 GH/s"},
		{"both fans run", map[string]any{"boardVersion": "702", "fan2rpm": 3900}, "healthy", 0, "fan", "ok", ""},
		{"first 10 minutes hashrate", map[string]any{"uptimeSeconds": 599, "hashRate_1h": 10.0}, "healthy", 0, "hashrate", "too_early", "uptime 599 s is below 600 s; too early to judge"},
		{"first 10 minutes shares", map[string]any{"uptimeSeconds": 599, "sharesAccepted": 10, "sharesRejected": 10}, "healthy", 0, "rejected_shares", "too_early", "uptime 599 s is below 600 s; too early to judge"},
		{"10 minutes of uptime", map[string]any{"uptimeSeconds": 600, "hashRate_1h": 10.0}, "unhealthy", 3, "hashrate", "failed", "1h hashrate is 1% of the expected hashrate"},
		{"zero shares", map[string]any{"sharesAccepted": 0, "sharesRejected": 0}, "healthy", 0, "rejected_shares", "too_early", "no share was submitted yet; too early to judge"},
		{"few shares", map[string]any{"sharesAccepted": 5, "sharesRejected": 5}, "healthy", 0, "rejected_shares", "too_early", "10 shares are below the minimum of 20; too early to judge"},
		{"fault wins over too early", map[string]any{"uptimeSeconds": 30, "miningPaused": true}, "unhealthy", 3, "mining_paused", "failed", "mining is paused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host, calls := miner(t, healthInfo(t, tc.changes))
			code, out := execute(t, New(func(string) string { return host }), "health")
			if code != tc.exit || !strings.HasPrefix(out, "verdict: "+tc.verdict+"\n") {
				t.Fatalf("code=%d\n%s", code, out)
			}
			row := ruleRow(t, out, tc.rule)
			if !strings.Contains(row, ","+tc.result+",") || (tc.reason != "" && !strings.HasSuffix(row, tc.reason)) || (tc.reason == "" && !strings.HasSuffix(row, ",null")) {
				t.Fatalf("row %q, want result %s reason %q", row, tc.result, tc.reason)
			}
			if got := strings.Join(calls(), ","); got != "GET /api/system/info" {
				t.Fatalf("requests=%s", got)
			}
		})
	}
}

func TestHealthOmittedFieldsAreUnknown(t *testing.T) {
	for _, tc := range []struct {
		omitted []string
		rules   []string
	}{
		{[]string{"overheat_mode"}, []string{"overheat_mode"}},
		{[]string{"miningPaused"}, []string{"mining_paused"}},
		{[]string{"isUsingFallbackStratum"}, []string{"fallback_pool"}},
		{[]string{"hashRate_1h"}, []string{"hashrate"}},
		{[]string{"expectedHashrate"}, []string{"hashrate"}},
		{[]string{"sharesAccepted"}, []string{"rejected_shares"}},
		{[]string{"sharesRejected"}, []string{"rejected_shares"}},
		{[]string{"fanrpm"}, []string{"fan"}},
		{[]string{"hashRate"}, []string{"fan"}},
		{[]string{"boardVersion"}, []string{"fan"}},
		{[]string{"uptimeSeconds"}, []string{"power_fault", "hardware_fault", "hashrate", "rejected_shares"}},
	} {
		t.Run(strings.Join(tc.omitted, ","), func(t *testing.T) {
			changes := map[string]any{}
			for _, name := range tc.omitted {
				changes[name] = deleted
			}
			host, _ := miner(t, healthInfo(t, changes))
			code, out := execute(t, New(func(string) string { return host }), "health")
			if code != 1 || !strings.HasPrefix(out, "verdict: unknown\nunknown: ") {
				t.Fatalf("code=%d\n%s", code, out)
			}
			for _, rule := range tc.rules {
				if row := ruleRow(t, out, rule); !strings.Contains(row, ",unknown,unknown,") {
					t.Errorf("row %q is not unknown", row)
				}
			}
		})
	}
}

func TestHealthSecondFanFieldOmittedIsUnknown(t *testing.T) {
	host, _ := miner(t, healthInfo(t, map[string]any{"boardVersion": "302", "fan2rpm": deleted}))
	code, out := execute(t, New(func(string) string { return host }), "health")
	if row := ruleRow(t, out, "fan"); code != 1 || !strings.Contains(row, ",unknown,unknown,") {
		t.Fatalf("code=%d row=%s", code, row)
	}
}

func TestHealthFailedWinsOverUnknown(t *testing.T) {
	host, _ := miner(t, healthInfo(t, map[string]any{"miningPaused": true, "fanrpm": deleted}))
	code, out := execute(t, New(func(string) string { return host }), "health")
	if code != 3 || !strings.HasPrefix(out, "verdict: unhealthy\nfailed: mining_paused\nunknown: fan\n") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestHealthJSON(t *testing.T) {
	host, _ := miner(t, healthInfo(t, map[string]any{"miningPaused": true}))
	code, out := execute(t, New(func(string) string { return host }), "health", "--json")
	if code != 3 || !strings.HasPrefix(out, `{"verdict":"unhealthy"`) {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestHealthReadFailureIsNotUnhealthy(t *testing.T) {
	code, out := execute(t, New(func(string) string { return "http://127.0.0.1:1" }), "health")
	if code != 1 || !strings.Contains(out, "miner_read_failed") || strings.Contains(out, "verdict") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestHealthUsageErrorsSendNoRequest(t *testing.T) {
	host, calls := miner(t, fixture(t, "info"))
	for _, args := range [][]string{{"health", "--bogus"}, {"health", "--fields", "hostname"}, {"health", "--confirm"}, {"health", "--limit", "5"}} {
		code, out := execute(t, New(func(string) string { return host }), args...)
		if code != 2 || !strings.Contains(out, "valid flags: --host, --json") {
			t.Errorf("%v: code=%d\n%s", args, code, out)
		}
	}
	if len(calls()) != 0 {
		t.Fatalf("requests=%v", calls())
	}
}

func TestHealthHelpStatesRulesAndLimits(t *testing.T) {
	code, out := execute(t, New(noHost), "health", "--help")
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	for _, want := range []string{"80%", "5%", "600 s", "20 shares", "exit 3", "unknown when no rule failed", "power_fault", "hardware_fault", "overheat_mode", "mining_paused", "fallback_pool", "hashrate", "rejected_shares", "fan2rpm", "no flag changes them"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q", want)
		}
	}
	_, home := execute(t, New(noHost), "--help")
	if !strings.Contains(home, "logs, health, discover") {
		t.Errorf("home help lacks health in the command list")
	}
}

func TestOnlyHealthPrintsAVerdict(t *testing.T) {
	host, _ := miner(t, healthInfo(t, map[string]any{"miningPaused": true}))
	for _, command := range []string{"", "info", "asic", "stats", "firmware", "scoreboard"} {
		args := []string{}
		if command != "" {
			args = append(args, command)
		}
		_, out := execute(t, New(func(string) string { return host }), args...)
		if strings.Contains(out, "verdict") {
			t.Errorf("%q prints a verdict:\n%s", command, out)
		}
	}
}
