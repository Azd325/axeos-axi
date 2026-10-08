package app

import (
	"context"
	"errors"
	"io"
	"math"
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
	// A numeric tuning value is 1 or more, so 0 is free for the word default.
	tuningDefault     = 0
	tuningDefaultWord = "default"
)

// The firmware reports the default of the ASIC model next to its list (GET_system_asic in ESP-Miner
// main/http_server/axe-os/api/system/asic_settings.c); each default in main/device_config.h is in its list.
type tuningSetting struct {
	flag, label, unit, view, field, options, factory string
}

var tuningSettings = []tuningSetting{
	{"--frequency", "frequency", "MHz", "frequency_mhz", "frequency", "frequencyOptions", "defaultFrequency"},
	{"--core-voltage", "core voltage", "mV", "core_voltage_set_mv", "coreVoltage", "voltageOptions", "defaultVoltage"},
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

func tuningArguments(opts options) string {
	var arguments string
	for _, s := range tuningSettings {
		value, named := opts.tuning[s.flag]
		if !named {
			continue
		}
		word := tuningDefaultWord
		if value != tuningDefault {
			word = strconv.Itoa(value)
		}
		arguments += " " + s.flag + " " + word
	}
	return arguments
}

type tuningChange struct {
	setting tuningSetting
	present float64
	new     int
}

type tuningPlan struct {
	settings map[string]int
	changes  []tuningChange
}

func (p *tuningPlan) previousArguments() string {
	var arguments string
	for _, c := range p.changes {
		arguments += " " + c.setting.flag + " " + formatNumber(c.present)
	}
	return arguments
}

func planTuning(ctx context.Context, client *axeos.Client, opts options, host string) (*tuningPlan, *viewFailure) {
	hostArg := shellQuote(host)
	command := "axeos-axi tuning --host " + hostArg
	lists := "axeos-axi asic --host " + hostArg + " --fields frequency,frequencyOptions,coreVoltage,voltageOptions shows the present values and the lists the miner reports"
	refuse := func(code, message, help string) (*tuningPlan, *viewFailure) {
		return nil, &viewFailure{exit: 1, code: code, message: message, help: help}
	}
	info, err := client.Get(ctx, "info")
	if err != nil {
		return nil, readFailed(err, connectivityHelp)
	}
	asic, err := client.Get(ctx, "asic")
	if err != nil {
		return nil, optionalViewFailure(err, "the list of allowed tuning values", host)
	}
	plan := &tuningPlan{settings: map[string]int{}}
	for _, s := range tuningSettings {
		value, named := opts.tuning[s.flag]
		if !named {
			continue
		}
		allowed, ok := allowedValues(asic[s.options])
		if !ok {
			return refuse("no_allowed_values", "the miner reports no list of allowed values for "+s.label+"; the write is refused", lists)
		}
		if value == tuningDefault {
			factory, ok := number(asic, s.factory)
			if !ok || factory < 1 || factory != math.Trunc(factory) {
				return refuse("no_default_value", "the miner reports no default value for "+s.label+"; the write is refused", "axeos-axi asic --host "+hostArg+" --fields "+s.factory+","+s.options+" shows the default and the list the miner reports")
			}
			value = int(factory)
		}
		if !slices.Contains(allowed, float64(value)) {
			names := make([]string, len(allowed))
			for i, n := range allowed {
				names[i] = formatNumber(n)
			}
			list := strings.Join(names, ", ")
			return nil, &viewFailure{exit: 1, code: "value_not_allowed", message: s.label + " " + strconv.Itoa(value) + " " + s.unit + " is not in the list the miner reports; allowed values in " + s.unit + ": " + list, help: command + " " + s.flag + " <value> with one allowed value", allowed: s.view + ": " + list}
		}
		present, ok := number(info, s.field)
		if !ok {
			return refuse("present_value_unknown", "the miner reports no present value for "+s.label+"; the write is refused", lists)
		}
		if !slices.Contains(allowed, present) {
			return refuse("not_reversible", "the present "+s.label+" "+formatNumber(present)+" "+s.unit+" is not in the list the miner reports, so this tool cannot set it again; the write is refused", lists)
		}
		plan.settings[s.field] = value
		plan.changes = append(plan.changes, tuningChange{setting: s, present: present, new: value})
	}
	return plan, nil
}

func tuning(ctx context.Context, client *axeos.Client, opts options, stdout io.Writer) int {
	hostArg := shellQuote(opts.host)
	command := "axeos-axi tuning --host " + hostArg
	plan, refused := planTuning(ctx, client, opts, opts.host)
	if refused != nil {
		return failure(stdout, refused.exit, refused.code, refused.message, refused.help)
	}
	var body output.Object
	var rows []any
	for _, c := range plan.changes {
		rows = append(rows, output.Object{{Name: "setting", Value: c.setting.view}, {Name: "present", Value: c.present}, {Name: "new", Value: c.new}, {Name: "changes", Value: c.present != float64(c.new)}})
		body = append(body, output.Field{Name: c.setting.field, Value: c.new})
	}
	fields := output.Object{{Name: "host", Value: opts.host}, {Name: "request", Value: tuningRequest}, {Name: "body", Value: body}}
	if !opts.confirm {
		return write(stdout, append(fields,
			output.Field{Name: "sent", Value: false},
			output.Field{Name: "settings", Value: rows},
			output.Field{Name: "effect", Value: tuningEffect},
			output.Field{Name: "execute", Value: command + tuningArguments(opts) + " --confirm"},
		))
	}
	verify := "axeos-axi asic --host " + hostArg + " shows frequency_mhz and core_voltage_set_mv"
	err := client.Patch(ctx, plan.settings)
	switch {
	case errors.Is(err, axeos.ErrNotSent):
		return failure(stdout, 1, "tuning_not_sent", err.Error(), connectivityHelp)
	case errors.Is(err, axeos.ErrNoAnswer):
		return failure(stdout, 1, "tuning_unconfirmed", err.Error()+"; the change is unconfirmed", verify+"; read them before another write")
	case err != nil:
		return failure(stdout, 1, "tuning_failed", err.Error()+"; the miner did not confirm the change", verify)
	}
	return write(stdout, append(fields,
		output.Field{Name: "sent", Value: true},
		output.Field{Name: "settings", Value: rows},
		output.Field{Name: "result", Value: "the miner accepted the request; " + tuningEffect},
		output.Field{Name: "help", Value: []any{verify, command + plan.previousArguments() + " --confirm sets the previous values again"}},
	))
}

func tuningHelp() output.Object {
	return output.Object{
		{Name: "command", Value: "tuning"},
		{Name: "description", Value: "Changes the miner: sets the ASIC frequency, the core voltage or both; " + tuningEffect + "; each call reads the present values and the allowed lists with GET /api/system/info and GET /api/system/asic; without --confirm sends no write request and prints the present value and the new value of each named setting and the command that performs the change; with --confirm sends exactly one " + tuningRequest + " that carries each named setting, also when a new value equals the present value; a value outside the list the miner reports is an error and sends no write request; the word default in place of a value is the default that the miner reports for that setting, and the preview and the result print its number; the command does not restart the miner; several miners in one call have their own result (see several_miners)"},
		{Name: "flags", Value: output.Object{
			{Name: "host", Value: writeHostsFlagHelp},
			{Name: "frequency", Value: "--frequency <MHz|default>; whole number from frequencyOptions of the miner, or default for its defaultFrequency"},
			{Name: "core_voltage", Value: "--core-voltage <mV|default>; whole number from voltageOptions of the miner, or default for its defaultVoltage"},
			{Name: "confirm", Value: "--confirm; sends the write request; default sends no write request"},
			{Name: "json", Value: jsonFlagHelp},
			{Name: "help", Value: "--help; no network request"},
			{Name: "version", Value: versionFlagHelp},
		}},
		{Name: "several_miners", Value: severalWritesHelp("tuning")},
		{Name: "timeout_s", Value: int(axeos.Timeout / time.Second)},
		{Name: "examples", Value: []any{"axeos-axi tuning --host 192.0.2.10 --frequency 525", "axeos-axi tuning --host 192.0.2.10 --frequency 525 --core-voltage 1150 --confirm", "axeos-axi tuning --host 192.0.2.10 --host 192.0.2.11 --frequency default", "axeos-axi tuning --help"}},
	}
}
