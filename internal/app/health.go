package app

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/Azd325/axeos-axi/internal/axeos"
	"github.com/Azd325/axeos-axi/internal/output"
)

const (
	healthExitUnhealthy   = 3
	minPerformanceUptimeS = 600
	minHashrateShare      = 0.8
	maxRejectedShare      = 0.05
	minSharesForRule      = 20
	healthTooEarly        = "too early to judge"
)

// Boards whose fan controller (EMC2302) reads a second fan: main/device_config.h and
// main/thermal/thermal.c in ESP-Miner, tags v2.15.3 and master. Every other board reports fan2rpm 0.
var twoFanBoards = []string{"302", "303", "701", "702"}

const (
	ruleOK       = "ok"
	ruleFailed   = "failed"
	ruleTooEarly = "too_early"
	ruleUnknown  = "unknown"
)

type ruleResult struct{ name, result, value, limit, reason string }

func (r ruleResult) row() output.Object {
	var reason any
	if r.reason != "" {
		reason = r.reason
	}
	return output.Object{{Name: "rule", Value: r.name}, {Name: "result", Value: r.result}, {Name: "value", Value: r.value}, {Name: "limit", Value: r.limit}, {Name: "reason", Value: reason}}
}

func unknownRule(name, limit, missing string) ruleResult {
	return ruleResult{name, ruleUnknown, "unknown", limit, "the miner did not send " + missing}
}

// The firmware sends power_fault and hardware_fault only while a fault is present,
// so an absent fault field means no fault once the telemetry group is present (uptimeSeconds).
func faultRule(name string, info map[string]any) ruleResult {
	const limit = "no fault"
	if _, ok := number(info, "uptimeSeconds"); !ok {
		return unknownRule(name, limit, "uptimeSeconds, which shows that the miner reports faults")
	}
	switch v := info[name].(type) {
	case nil:
		return ruleResult{name, ruleOK, "none", limit, ""}
	case string:
		if v == "" {
			return ruleResult{name, ruleOK, "none", limit, ""}
		}
		return ruleResult{name, ruleFailed, v, limit, "the miner reports a " + strings.ReplaceAll(name, "_", " ")}
	}
	return ruleResult{name, ruleFailed, "reported", limit, "the miner reports a " + strings.ReplaceAll(name, "_", " ")}
}

func stateRule(name, field, bad, good, reason string, info map[string]any) ruleResult {
	v, ok := flag(info, field).(bool)
	if !ok {
		return unknownRule(name, good, field)
	}
	if v {
		return ruleResult{name, ruleFailed, bad, good, reason}
	}
	return ruleResult{name, ruleOK, good, good, ""}
}

func performanceUptime(info map[string]any) (float64, bool) {
	uptime, ok := number(info, "uptimeSeconds")
	return uptime, ok
}

func earlyReason(uptime float64) string {
	return fmt.Sprintf("uptime %.0f s is below %d s; %s", uptime, minPerformanceUptimeS, healthTooEarly)
}

func hashrateRule(info map[string]any) ruleResult {
	const name = "hashrate"
	limit := fmt.Sprintf("1h hashrate at least %.0f%% of expected hashrate", minHashrateShare*100)
	uptime, uptimeOK := performanceUptime(info)
	hour, hourOK := number(info, "hashRate_1h")
	expected, expectedOK := number(info, "expectedHashrate")
	switch {
	case !uptimeOK:
		return unknownRule(name, limit, "uptimeSeconds")
	case !hourOK:
		return unknownRule(name, limit, "hashRate_1h")
	case !expectedOK || expected <= 0:
		return unknownRule(name, limit, "a positive expectedHashrate")
	}
	value := fmt.Sprintf("%.2f GH/s", hour)
	limit = fmt.Sprintf("at least %.2f GH/s (%.0f%% of expected %.2f GH/s)", expected*minHashrateShare, minHashrateShare*100, expected)
	switch {
	case uptime < minPerformanceUptimeS:
		return ruleResult{name, ruleTooEarly, value, limit, earlyReason(uptime)}
	case hour < expected*minHashrateShare:
		return ruleResult{name, ruleFailed, value, limit, fmt.Sprintf("1h hashrate is %.0f%% of the expected hashrate", hour/expected*100)}
	}
	return ruleResult{name, ruleOK, value, limit, ""}
}

func rejectedSharesRule(info map[string]any) ruleResult {
	const name = "rejected_shares"
	limit := fmt.Sprintf("at most %.0f%% of all shares", maxRejectedShare*100)
	uptime, uptimeOK := performanceUptime(info)
	accepted, acceptedOK := number(info, "sharesAccepted")
	rejected, rejectedOK := number(info, "sharesRejected")
	switch {
	case !uptimeOK:
		return unknownRule(name, limit, "uptimeSeconds")
	case !acceptedOK:
		return unknownRule(name, limit, "sharesAccepted")
	case !rejectedOK:
		return unknownRule(name, limit, "sharesRejected")
	}
	total := accepted + rejected
	if total == 0 {
		return ruleResult{name, ruleTooEarly, "0 of 0 shares", limit, "no share was submitted yet; " + healthTooEarly}
	}
	value := fmt.Sprintf("%.0f of %.0f shares (%.1f%%)", rejected, total, rejected/total*100)
	switch {
	case uptime < minPerformanceUptimeS:
		return ruleResult{name, ruleTooEarly, value, limit, earlyReason(uptime)}
	case total < minSharesForRule:
		return ruleResult{name, ruleTooEarly, value, limit, fmt.Sprintf("%.0f shares are below the minimum of %d; %s", total, minSharesForRule, healthTooEarly)}
	case rejected/total > maxRejectedShare:
		return ruleResult{name, ruleFailed, value, limit, fmt.Sprintf("%.1f%% of all shares are rejected", rejected/total*100)}
	}
	return ruleResult{name, ruleOK, value, limit, ""}
}

func fanRule(info map[string]any) ruleResult {
	const name, limit = "fan", "above 0 rpm while the miner hashes"
	hashrate, hashOK := number(info, "hashRate")
	fan1, fan1OK := number(info, "fanrpm")
	switch {
	case !hashOK:
		return unknownRule(name, limit, "hashRate")
	case !fan1OK:
		return unknownRule(name, limit, "fanrpm")
	}
	board, boardOK := info["boardVersion"].(string)
	if !boardOK || board == "" {
		return unknownRule(name, limit, "boardVersion, which shows whether the board has a second fan")
	}
	value := fmt.Sprintf("%.0f rpm", fan1)
	fans := []float64{fan1}
	if slices.Contains(twoFanBoards, board) {
		fan2, ok := number(info, "fan2rpm")
		if !ok {
			return unknownRule(name, limit, "fan2rpm")
		}
		value = fmt.Sprintf("fan 1 %.0f rpm; fan 2 %.0f rpm", fan1, fan2)
		fans = append(fans, fan2)
	}
	if hashrate > 0 && slices.Contains(fans, 0) {
		return ruleResult{name, ruleFailed, value, limit, fmt.Sprintf("a fan reads 0 rpm while the miner hashes at %.2f GH/s", hashrate)}
	}
	return ruleResult{name, ruleOK, value, limit, ""}
}

func healthRules(info map[string]any) []ruleResult {
	return []ruleResult{
		faultRule("power_fault", info),
		faultRule("hardware_fault", info),
		stateRule("overheat_mode", "overheat_mode", "active", "inactive", "overheat mode is active", info),
		stateRule("mining_paused", "miningPaused", "paused", "running", "mining is paused", info),
		stateRule("fallback_pool", "isUsingFallbackStratum", "fallback pool", "primary pool", "the miner runs on the fallback pool", info),
		hashrateRule(info),
		rejectedSharesRule(info),
		fanRule(info),
	}
}

func verdict(rules []ruleResult) (string, int) {
	counts := map[string]int{}
	for _, r := range rules {
		counts[r.result]++
	}
	switch {
	case counts[ruleFailed] > 0:
		return "unhealthy", healthExitUnhealthy
	case counts[ruleUnknown] > 0:
		return "unknown", 1
	}
	return "healthy", 0
}

func health(ctx context.Context, client *axeos.Client, opts options, stdout io.Writer) int {
	info, err := client.Get(ctx, "info")
	if err != nil {
		return failure(stdout, 1, "miner_read_failed", err.Error(), "check --host or AXEOS_HOST and local network connectivity")
	}
	rules := healthRules(info)
	state, exit := verdict(rules)
	rows := make([]any, len(rules))
	var failed, unknown, early []string
	for i, r := range rules {
		rows[i] = r.row()
		switch r.result {
		case ruleFailed:
			failed = append(failed, r.name)
		case ruleUnknown:
			unknown = append(unknown, r.name)
		case ruleTooEarly:
			early = append(early, r.name)
		}
	}
	fields := output.Object{{Name: "verdict", Value: state}}
	if len(failed) != 0 {
		fields = append(fields, output.Field{Name: "failed", Value: strings.Join(failed, ",")})
	}
	if len(unknown) != 0 {
		fields = append(fields, output.Field{Name: "unknown", Value: strings.Join(unknown, ",")})
	}
	if len(early) != 0 {
		fields = append(fields, output.Field{Name: "too_early", Value: strings.Join(early, ",")})
	}
	fields = append(fields, output.Field{Name: "rules", Value: rows})
	if state != "healthy" {
		fields = append(fields, output.Field{Name: "help", Value: []any{"axeos-axi --host " + shellQuote(opts.host) + " for the home view of the miner"}})
	}
	if write(stdout, fields) != 0 {
		return 1
	}
	return exit
}

func healthHelp() output.Object {
	return output.Object{
		{Name: "command", Value: "health"},
		{Name: "description", Value: "Judges one miner by fixed rules; sends exactly one request, GET /api/system/info; prints the verdict and one row for each rule with its result, the value read, the limit used and, for a rule that is not ok, the reason; no other command prints a verdict; the limits are fixed and no flag changes them"},
		{Name: "verdict", Value: "unhealthy when any rule failed, exit 3; unknown when no rule failed and at least one rule is unknown, exit 1, because a missing value is never read as healthy; healthy otherwise, exit 0, also when a rule is too_early"},
		{Name: "rules", Value: output.Object{
			{Name: "power_fault", Value: "failed when the miner reports a power fault; no limit; the firmware sends the field only during a fault, so a missing field is ok when the miner sends uptimeSeconds"},
			{Name: "hardware_fault", Value: "failed when the miner reports a hardware fault; no limit; same rule for a missing field as power_fault"},
			{Name: "overheat_mode", Value: "failed when overheat mode is active (overheat_mode is 1)"},
			{Name: "mining_paused", Value: "failed when mining is paused (miningPaused is true)"},
			{Name: "fallback_pool", Value: "failed when the miner runs on the fallback pool (isUsingFallbackStratum is 1)"},
			{Name: "hashrate", Value: fmt.Sprintf("failed when hashRate_1h is below %.0f%% of expectedHashrate; too_early in the first %d s of uptime; unknown without a positive expectedHashrate", minHashrateShare*100, minPerformanceUptimeS)},
			{Name: "rejected_shares", Value: fmt.Sprintf("failed when more than %.0f%% of all shares (sharesRejected of sharesAccepted plus sharesRejected) are rejected; too_early in the first %d s of uptime, with 0 shares and with fewer than %d shares; the firmware resets the counters when it switches to the fallback pool", maxRejectedShare*100, minPerformanceUptimeS, minSharesForRule)},
			{Name: "fan", Value: "failed when fanrpm is 0 while hashRate is above 0; a board with a second fan (boardVersion " + strings.Join(twoFanBoards, ", ") + ") checks fan2rpm too"},
		}},
		{Name: "results", Value: "ok, failed, too_early (not judged yet, the reason names the limit that is not met), unknown (the miner did not send a field the rule needs)"},
		{Name: "exit_codes", Value: "0 healthy, 3 unhealthy, 1 unknown verdict or a failed read, 2 usage error"},
		{Name: "flags", Value: output.Object{
			{Name: "host", Value: hostFlagHelp},
			{Name: "json", Value: jsonFlagHelp},
			{Name: "help", Value: "--help; no network request"},
			{Name: "version", Value: versionFlagHelp},
		}},
		{Name: "timeout_s", Value: 4},
		{Name: "examples", Value: []any{"axeos-axi health --host 192.0.2.10", "axeos-axi health --host 192.0.2.10 --json", "axeos-axi health --help"}},
	}
}
