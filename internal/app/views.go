package app

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/Azd325/axeos-axi/internal/output"
)

func number(m map[string]any, key string) (float64, bool) {
	n, ok := m[key].(float64)
	return n, ok && !math.IsNaN(n) && !math.IsInf(n, 0)
}

func measure(m map[string]any, key, unit string) string {
	if n, ok := number(m, key); ok && n >= 0 {
		return fmt.Sprintf("%.2f %s", n, unit)
	}
	return "unknown"
}

func count(m map[string]any, key string) string {
	if n, ok := number(m, key); ok && n >= 0 {
		return fmt.Sprintf("%.0f", n)
	}
	return "unknown"
}

func text(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok && s != "" {
		return s
	}
	return "unknown"
}

func flag(m map[string]any, key string) any {
	if b, ok := m[key].(bool); ok {
		return b
	}
	if n, ok := number(m, key); ok {
		return n != 0
	}
	return nil
}

func efficiency(m map[string]any) any {
	p, powerOK := number(m, "power")
	h, hashOK := number(m, "hashRate")
	if !powerOK || !hashOK || h <= 0 || p < 0 {
		return nil
	}
	return math.Round(p*1000/h*100) / 100
}

func homeView(m map[string]any) output.Object {
	eff := "unknown"
	if n, ok := efficiency(m).(float64); ok {
		eff = fmt.Sprintf("%.2f J/TH", n)
	}
	poolURL := text(m, "stratumURL")
	if flag(m, "isUsingFallbackStratum") == true {
		poolURL = text(m, "fallbackStratumURL")
	}
	return output.Object{
		{Name: "hostname", Value: m["hostname"]},
		{Name: "model", Value: m["ASICModel"]},
		{Name: "firmware", Value: m["version"]},
		{Name: "hashrate", Value: "current=" + measure(m, "hashRate", "GH/s") + "; 1h=" + measure(m, "hashRate_1h", "GH/s") + "; expected=" + measure(m, "expectedHashrate", "GH/s")},
		{Name: "temperature", Value: "chip=" + measure(m, "temp", "C") + "; regulator=" + measure(m, "vrTemp", "C")},
		{Name: "power_w", Value: m["power"]},
		{Name: "efficiency", Value: eff},
		{Name: "fan", Value: "speed=" + measure(m, "fanspeed", "%") + "; rpm=" + count(m, "fanrpm")},
		{Name: "pool", Value: poolURL},
		{Name: "pool_connection", Value: m["poolConnectionInfo"]},
		{Name: "pool_fallback", Value: flag(m, "isUsingFallbackStratum")},
		{Name: "shares", Value: "accepted=" + count(m, "sharesAccepted") + "; rejected=" + count(m, "sharesRejected")},
		{Name: "best_difficulty", Value: "all_time=" + count(m, "bestDiff") + "; session=" + count(m, "bestSessionDiff")},
		{Name: "uptime_s", Value: m["uptimeSeconds"]},
		{Name: "overheat", Value: flag(m, "overheat_mode")},
		{Name: "paused", Value: flag(m, "miningPaused")},
	}
}

func infoView(m map[string]any) output.Object {
	return output.Object{
		{Name: "hostname", Value: m["hostname"]}, {Name: "asic_model", Value: m["ASICModel"]},
		{Name: "firmware", Value: m["version"]}, {Name: "axeos_version", Value: m["axeOSVersion"]},
		{Name: "idf_version", Value: m["idfVersion"]}, {Name: "board", Value: m["boardVersion"]},
		{Name: "heap_free_bytes", Value: m["freeHeap"]}, {Name: "heap_internal_free_bytes", Value: m["freeHeapInternal"]},
		{Name: "heap_min_free_bytes", Value: m["minFreeHeap"]}, {Name: "heap_max_alloc_bytes", Value: m["maxAllocHeap"]},
		{Name: "wifi_state", Value: m["wifiStatus"]}, {Name: "wifi_signal_dbm", Value: m["wifiRSSI"]},
		{Name: "uptime_s", Value: m["uptimeSeconds"]}, {Name: "reset_reason", Value: m["resetReason"]},
		{Name: "partition", Value: m["runningPartition"]},
	}
}

func asicView(m map[string]any) output.Object {
	fanMode := "unknown"
	if flag(m, "autofanspeed") == true {
		fanMode = "auto"
	} else if flag(m, "autofanspeed") == false {
		fanMode = "manual"
	}
	return output.Object{
		{Name: "model", Value: m["ASICModel"]}, {Name: "device_model", Value: m["deviceModel"]},
		{Name: "asic_count", Value: m["asicCount"]}, {Name: "hash_domains", Value: m["hashDomains"]},
		{Name: "frequency_mhz", Value: m["frequency"]}, {Name: "frequency_actual_mhz", Value: m["actualFrequency"]},
		{Name: "frequency_default_mhz", Value: m["defaultFrequency"]},
		{Name: "core_voltage_set_mv", Value: m["coreVoltage"]}, {Name: "core_voltage_actual_mv", Value: m["coreVoltageActual"]},
		{Name: "core_voltage_default_mv", Value: m["defaultVoltage"]},
		{Name: "fan_mode", Value: fanMode}, {Name: "fan_manual_pct", Value: m["manualFanSpeed"]},
		{Name: "temperature_target_c", Value: m["temptarget"]},
	}
}

func firmwareView(m map[string]any) output.Object {
	return output.Object{
		{Name: "partition", Value: m["partition"]}, {Name: "version", Value: m["version"]},
		{Name: "size_bytes", Value: m["size"]}, {Name: "sha256", Value: m["sha256"]},
	}
}

func statsView(info, stats map[string]any) (output.Object, error) {
	frequency, frequencyOK := number(info, "statsFrequency")
	if frequencyOK && frequency == 0 {
		return output.Object{{Name: "state", Value: "statistics logging disabled on miner (statsFrequency=0); 0 recorded samples available"}, {Name: "sample_count", Value: 0}, {Name: "logging_interval_s", Value: 0}}, nil
	}
	labels, labelsOK := stats["labels"].([]any)
	rows, rowsOK := stats["statistics"].([]any)
	if !labelsOK || !rowsOK {
		return nil, errors.New("statistics response lacks labels or samples")
	}
	seen := map[string]bool{}
	for _, label := range labels {
		name, ok := label.(string)
		if !ok || name == "" || seen[name] {
			return nil, errors.New("statistics response contains invalid or duplicate labels")
		}
		seen[name] = true
	}
	latest := map[string]any{}
	for _, row := range rows {
		values, ok := row.([]any)
		if !ok || len(values) != len(labels) {
			return nil, errors.New("statistics sample does not match its labels")
		}
		sample := map[string]any{}
		for i, label := range labels {
			sample[label.(string)] = values[i]
		}
		stamp, ok := number(sample, "timestamp")
		if !ok {
			return nil, errors.New("statistics sample lacks a numeric timestamp")
		}
		prev, exists := number(latest, "timestamp")
		if !exists || stamp >= prev {
			latest = sample
		}
	}
	if len(rows) == 0 {
		return output.Object{{Name: "state", Value: "0 recorded statistics samples found on miner"}, {Name: "sample_count", Value: 0}, {Name: "logging_interval_s", Value: info["statsFrequency"]}}, nil
	}
	return output.Object{
		{Name: "sample_count", Value: len(rows)}, {Name: "logging_interval_s", Value: info["statsFrequency"]},
		{Name: "current_timestamp_ms", Value: stats["currentTimestamp"]}, {Name: "latest_timestamp_ms", Value: latest["timestamp"]},
		{Name: "hashrate_ghs", Value: latest["hashrate"]}, {Name: "hashrate_1h_ghs", Value: latest["hashrate_1h"]},
		{Name: "chip_temperature_c", Value: latest["asicTemp"]}, {Name: "regulator_temperature_c", Value: latest["vrTemp"]},
		{Name: "power_w", Value: latest["power"]},
	}, nil
}

func viewNames(command string) string {
	var fields output.Object
	switch command {
	case "info":
		fields = infoView(nil)
	case "asic":
		fields = asicView(nil)
	case "firmware":
		fields = firmwareView(nil)
	case "stats":
		fields, _ = statsView(map[string]any{"statsFrequency": float64(120)}, map[string]any{"labels": []any{"timestamp"}, "statistics": []any{[]any{float64(0)}}})
	default:
		fields = homeView(nil)
	}
	names := make([]string, 0, len(fields))
	for _, f := range fields {
		names = append(names, f.Name)
	}
	if command == "stats" {
		names = append(names, "state")
	}
	return strings.Join(names, ",")
}

func exampleFields(command string) string {
	switch command {
	case "info":
		return "firmware,board,heap_free_bytes"
	case "asic":
		return "frequency_mhz,core_voltage_actual_mv"
	case "stats":
		return "sample_count,power_w"
	case "firmware":
		return "version,sha256"
	default:
		return "hashrate,temperature,power_w"
	}
}
