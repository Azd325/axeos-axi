package app

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/Azd325/axeos-axi/internal/axeos"
	"github.com/Azd325/axeos-axi/internal/output"
)

const (
	severalHostsCommands = "the home view, `info`, `asic`, `stats`, `firmware`, `restart`, `tuning` and `pool`"
	maxFailureHelp       = 3
)

type viewFailure struct {
	exit                int
	code, message, help string
	allowed             string
}

type minerView struct {
	fields output.Object
	raw    map[string]any
}

func optionalViewFailure(err error, subject, host string) *viewFailure {
	if errors.Is(err, axeos.ErrNotFound) || errors.Is(err, axeos.ErrRootRedirect) {
		return &viewFailure{exit: 1, code: "not_supported", message: subject + " is not supported by this firmware", help: "axeos-axi info --host " + shellQuote(host) + " shows the firmware version"}
	}
	return &viewFailure{exit: 1, code: "miner_read_failed", message: err.Error(), help: connectivityHelp}
}

func readFailed(err error, help string) *viewFailure {
	return &viewFailure{exit: 1, code: "miner_read_failed", message: err.Error(), help: help}
}

func readView(ctx context.Context, client *axeos.Client, host string, opts options) (*minerView, *viewFailure) {
	var info map[string]any
	var err error
	if opts.command != "firmware" {
		info, err = client.Get(ctx, "info")
		if err != nil {
			return nil, readFailed(err, connectivityHelp)
		}
	}
	raw := info
	var fields output.Object
	switch opts.command {
	case "firmware":
		checksum, readErr := client.Get(ctx, "firmware/checksum")
		if readErr != nil {
			return nil, optionalViewFailure(readErr, "firmware checksum", host)
		}
		raw = checksum
		fields = firmwareView(checksum)
	case "info":
		fields = infoView(info)
	case "asic":
		asic, readErr := client.Get(ctx, "asic")
		if readErr != nil {
			return nil, readFailed(readErr, "check the miner API with axeos-axi info")
		}
		raw = make(map[string]any, len(info)+len(asic))
		for k, v := range info {
			raw[k] = v
		}
		for k, v := range asic {
			raw[k] = v
		}
		fields = asicView(raw)
	case "stats":
		var stats map[string]any
		var readErr error
		if len(opts.columns) != 0 {
			stats, readErr = client.GetStatistics(ctx, opts.columns)
		} else {
			stats, readErr = client.Get(ctx, "statistics")
		}
		if readErr != nil {
			return nil, readFailed(readErr, "check the miner API with axeos-axi info")
		}
		raw = stats
		if opts.samples != 0 {
			fields, err = statsHistoryView(info, stats, opts.columns, opts.samples, shellQuote(host))
		} else {
			fields, err = statsView(info, stats)
		}
		if err != nil {
			return nil, &viewFailure{exit: 1, code: "invalid_statistics", message: err.Error(), help: "check AxeOS statistics API compatibility"}
		}
	default:
		fields = homeView(info)
	}
	return &minerView{fields: fields, raw: raw}, nil
}

func (v *minerView) available(command string) map[string]any {
	available := map[string]any{}
	for _, name := range strings.Split(viewNames(command), ",") {
		available[name] = nil
	}
	if readsInfoFields(command) {
		for _, name := range conditionalInfoFields {
			available[name] = nil
		}
	}
	for k, val := range v.raw {
		available[k] = val
	}
	for _, f := range v.fields {
		available[f.Name] = f.Value
	}
	return available
}

// selected returns the fields to print and, when --fields names a field the miner does not know, that name.
func (v *minerView) selected(opts options) (output.Object, string) {
	if len(opts.fields) == 0 {
		return v.fields, ""
	}
	selected := make(output.Object, 0, len(opts.fields))
	available := v.available(opts.command)
	for _, name := range opts.fields {
		value, ok := available[name]
		if !ok {
			return nil, name
		}
		selected = append(selected, output.Field{Name: name, Value: value})
	}
	if opts.command == "stats" && available["state"] != nil {
		if !slices.Contains(opts.fields, "state") {
			selected = append(output.Object{{Name: "state", Value: available["state"]}}, selected...)
		}
	}
	return selected, ""
}

func severalHostsError(opts options, tableStats bool) error {
	switch opts.command {
	case "", "info", "asic", "firmware", "restart", "tuning", "pool", "discover", "skill", "host", "setup", "session":
		return nil
	case "stats":
		if !tableStats {
			return nil
		}
		return errors.New("--host was given more than once; `stats --samples` and `stats --columns` print a table of samples and take one miner; several miners are accepted by " + severalHostsCommands + ", without --samples and --columns")
	}
	return errors.New("--host was given more than once; `" + opts.command + "` takes one miner; several miners are accepted by " + severalHostsCommands)
}

type minerResult struct {
	view    *minerView
	failure *viewFailure
}

func readMiners(ctx context.Context, clients []*axeos.Client, opts options, stdout io.Writer) int {
	results := make([]minerResult, len(clients))
	var wg sync.WaitGroup
	for i, client := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i].view, results[i].failure = readView(ctx, client, opts.hosts[i], opts)
		}()
	}
	wg.Wait()

	var columns []string
	if len(opts.fields) == 0 {
		columns = strings.Split(viewNames(opts.command), ",")
	} else {
		columns = slices.Clone(opts.fields)
	}
	hasState := false
	failed := 0
	answered := 0
	for _, r := range results {
		if r.failure != nil {
			failed++
			continue
		}
		answered++
		hasState = hasState || r.view.available(opts.command)["state"] != nil
	}
	if len(opts.fields) != 0 {
		if answered != 0 {
			for _, name := range opts.fields {
				known := slices.ContainsFunc(results, func(r minerResult) bool {
					if r.failure != nil {
						return false
					}
					_, ok := r.view.available(opts.command)[name]
					return ok
				})
				if !known {
					return failure(stdout, 2, "unknown_field", "unknown field "+name, strings.TrimSpace("axeos-axi "+opts.command)+" --help; valid fields: "+viewNames(opts.command)+" (or exact API field names)")
				}
			}
		}
		if opts.command == "stats" && hasState && !slices.Contains(columns, "state") {
			columns = append([]string{"state"}, columns...)
		}
	}

	rows := make([]any, len(results))
	var failureHelp []any
	for i, r := range results {
		row := make(output.Object, 0, len(columns)+2)
		row = append(row, output.Field{Name: "host", Value: opts.hosts[i]})
		var available map[string]any
		if r.failure == nil {
			available = r.view.available(opts.command)
		}
		for _, name := range columns {
			row = append(row, output.Field{Name: name, Value: available[name]})
		}
		var code any
		if r.failure != nil {
			code = r.failure.code
			if len(failureHelp) < maxFailureHelp {
				failureHelp = append(failureHelp, strings.TrimSpace("axeos-axi "+opts.command)+" --host "+shellQuote(opts.hosts[i])+" for the error message of that miner")
			}
		}
		rows[i] = append(row, output.Field{Name: "error", Value: code})
	}

	var fields output.Object
	if opts.command == "" {
		fields = identity()
	}
	fields = append(fields, output.Field{Name: "count", Value: len(results)}, output.Field{Name: "failed", Value: failed}, output.Field{Name: "miners", Value: rows})
	help := failureHelp
	if opts.command == "" && len(opts.fields) == 0 {
		var hostArgs []string
		for _, host := range opts.hosts {
			hostArgs = append(hostArgs, "--host "+shellQuote(host))
		}
		help = append(help, "axeos-axi info "+strings.Join(hostArgs, " ")+" for system and network detail of the same miners")
	}
	if len(help) != 0 {
		fields = append(fields, output.Field{Name: "help", Value: help})
	}
	if write(stdout, fields) != 0 {
		return 1
	}
	if failed != 0 {
		return 1
	}
	return 0
}
