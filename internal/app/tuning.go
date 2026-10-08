package app

import (
	"context"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Azd325/axeos-axi/internal/axeos"
	"github.com/Azd325/axeos-axi/internal/output"
)

const (
	tuningRequest = "PATCH /api/system"
	// The power management task applies both values from NVS in each cycle (POWER_MANAGEMENT_task in ESP-Miner main/tasks/power_management_task.c).
	tuningEffect = "the values are active at once, without a restart, and the miner keeps them across a restart"
	tuningUsage  = "tuning requires --frequency <MHz>, --core-voltage <mV> or both"
)

type tuningSetting struct {
	flag, label, unit, view, field, options string
}

var tuningSettings = []tuningSetting{
	{"--frequency", "frequency", "MHz", "frequency_mhz", "frequency", "frequencyOptions"},
	{"--core-voltage", "core voltage", "mV", "core_voltage_set_mv", "coreVoltage", "voltageOptions"},
}

func isTuningFlag(flag string) bool {
	return slices.ContainsFunc(tuningSettings, func(s tuningSetting) bool { return s.flag == flag })
}

func allowedValues(v any) ([]float64, bool) {
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		return nil, false
	}
	values := make([]float64, len(list))
	for i, item := range list {
		n, ok := item.(float64)
		if !ok {
			return nil, false
		}
		values[i] = n
	}
	return values, true
}

func formatNumber(n float64) string { return strconv.FormatFloat(n, 'f', -1, 64) }

func tuning(ctx context.Context, client *axeos.Client, opts options, stdout io.Writer) int {
	hostArg := shellQuote(opts.host)
	command := "axeos-axi tuning --host " + hostArg
	lists := "axeos-axi asic --host " + hostArg + " --fields frequency,frequencyOptions,coreVoltage,voltageOptions shows the present values and the lists the miner reports"
	info, err := client.Get(ctx, "info")
	if err != nil {
		return failure(stdout, 1, "miner_read_failed", err.Error(), "check --host or AXEOS_HOST and local network connectivity")
	}
	asic, err := client.Get(ctx, "asic")
	if err != nil {
		return optionalReadFailure(stdout, err, "the list of allowed tuning values", opts.host)
	}
	settings := map[string]int{}
	var body output.Object
	var rows []any
	execute, revert := command, command
	for _, s := range tuningSettings {
		value, named := opts.tuning[s.flag]
		if !named {
			continue
		}
		allowed, ok := allowedValues(asic[s.options])
		if !ok {
			return failure(stdout, 1, "no_allowed_values", "the miner reports no list of allowed values for "+s.label+"; the write is refused", lists)
		}
		if !slices.Contains(allowed, float64(value)) {
			names := make([]string, len(allowed))
			for i, n := range allowed {
				names[i] = formatNumber(n)
			}
			return failure(stdout, 1, "value_not_allowed", s.label+" "+strconv.Itoa(value)+" "+s.unit+" is not in the list the miner reports; allowed values in "+s.unit+": "+strings.Join(names, ", "), command+" "+s.flag+" <value> with one allowed value")
		}
		present, ok := number(info, s.field)
		if !ok {
			return failure(stdout, 1, "present_value_unknown", "the miner reports no present value for "+s.label+"; the write is refused", lists)
		}
		rows = append(rows, output.Object{{Name: "setting", Value: s.view}, {Name: "present", Value: present}, {Name: "new", Value: value}, {Name: "changes", Value: present != float64(value)}})
		if !slices.Contains(allowed, present) {
			return failure(stdout, 1, "not_reversible", "the present "+s.label+" "+formatNumber(present)+" "+s.unit+" is not in the list the miner reports, so this tool cannot set it again; the write is refused", lists)
		}
		settings[s.field] = value
		body = append(body, output.Field{Name: s.field, Value: value})
		execute += " " + s.flag + " " + strconv.Itoa(value)
		revert += " " + s.flag + " " + formatNumber(present)
	}
	fields := output.Object{{Name: "host", Value: opts.host}, {Name: "request", Value: tuningRequest}, {Name: "body", Value: body}}
	if !opts.confirm {
		return write(stdout, append(fields,
			output.Field{Name: "sent", Value: false},
			output.Field{Name: "settings", Value: rows},
			output.Field{Name: "effect", Value: tuningEffect},
			output.Field{Name: "execute", Value: execute + " --confirm"},
		))
	}
	verify := "axeos-axi asic --host " + hostArg + " shows frequency_mhz and core_voltage_set_mv"
	err = client.Patch(ctx, settings)
	switch {
	case errors.Is(err, axeos.ErrNotSent):
		return failure(stdout, 1, "tuning_not_sent", err.Error(), "check --host or AXEOS_HOST and local network connectivity")
	case errors.Is(err, axeos.ErrNoAnswer):
		return failure(stdout, 1, "tuning_unconfirmed", err.Error()+"; the change is unconfirmed", verify+"; read them before another write")
	case err != nil:
		return failure(stdout, 1, "tuning_failed", err.Error()+"; the miner did not confirm the change", verify)
	}
	return write(stdout, append(fields,
		output.Field{Name: "sent", Value: true},
		output.Field{Name: "settings", Value: rows},
		output.Field{Name: "result", Value: "the miner accepted the request; " + tuningEffect},
		output.Field{Name: "help", Value: []any{verify, revert + " --confirm sets the previous values again"}},
	))
}

func tuningHelp() output.Object {
	return output.Object{
		{Name: "command", Value: "tuning"},
		{Name: "description", Value: "Changes the miner: sets the ASIC frequency, the core voltage or both; " + tuningEffect + "; each call reads the present values and the allowed lists with GET /api/system/info and GET /api/system/asic; without --confirm sends no write request and prints the present value and the new value of each named setting and the command that performs the change; with --confirm sends exactly one " + tuningRequest + " that carries each named setting, also when a new value equals the present value; a value outside the list the miner reports is an error and sends no write request; the command does not restart the miner"},
		{Name: "flags", Value: output.Object{
			{Name: "host", Value: hostFlagHelp},
			{Name: "frequency", Value: "--frequency <MHz>; whole number from frequencyOptions of the miner"},
			{Name: "core_voltage", Value: "--core-voltage <mV>; whole number from voltageOptions of the miner"},
			{Name: "confirm", Value: "--confirm; sends the write request; default sends no write request"},
			{Name: "json", Value: jsonFlagHelp},
			{Name: "help", Value: "--help; no network request"},
			{Name: "version", Value: versionFlagHelp},
		}},
		{Name: "timeout_s", Value: int(axeos.Timeout / time.Second)},
		{Name: "examples", Value: []any{"axeos-axi tuning --host 192.0.2.10 --frequency 525", "axeos-axi tuning --host 192.0.2.10 --frequency 525 --core-voltage 1150 --confirm", "axeos-axi tuning --help"}},
	}
}
