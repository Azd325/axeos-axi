package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Azd325/axeos-axi/internal/axeos"
	"github.com/Azd325/axeos-axi/internal/mdns"
	"github.com/Azd325/axeos-axi/internal/output"
)

const (
	universalFlags = "--help, -v, -V, --version"
	hostFlagHelp   = "--host <address>; default AXEOS_HOST; required for reads; HTTP unless a scheme is supplied"
)

var commandNames = []string{"info", "asic", "stats", "firmware", "scoreboard", "logs", "discover", "restart", "tuning", "pool", "skill"}

func commands() string { return strings.Join(commandNames, ", ") }

func validFlags(command string) string {
	switch command {
	case "skill":
		return "--path, " + universalFlags
	case "discover":
		return "--timeout, --fields, " + universalFlags
	case "logs":
		return "--host, --lines, --show-private, " + universalFlags
	case "restart":
		return "--host, --confirm, " + universalFlags
	case "tuning":
		return "--host, --frequency, --core-voltage, --confirm, " + universalFlags
	case "pool":
		return "--host, --url, --port, --user, --fallback-url, --fallback-port, --fallback-user, --show-user, --confirm, " + universalFlags
	}
	return "--host, --fields, " + universalFlags
}

type Browser interface {
	Browse(ctx context.Context, service string, wait time.Duration) ([]mdns.Service, error)
}

type App struct {
	getenv  func(string) string
	Version string
	Browser Browser
}

func New(getenv func(string) string) *App {
	return &App{getenv: getenv, Version: "dev", Browser: mdns.Multicast{}}
}

type options struct {
	command, host, action  string
	path                   string
	timeout, lines         int
	fields                 []string
	tuning                 map[string]int
	pool                   map[string]any
	help, version, confirm bool
	showUser, showPrivate  bool
}

// The command may follow its flags, so parsing continues after the first error
// to report the valid flags of the right command.
func parse(args []string) (options, error) {
	opts := options{timeout: defaultDiscoverTimeout, tuning: map[string]int{}, pool: map[string]any{}}
	var first error
	fail := func(format string, a ...any) {
		if first == nil {
			first = fmt.Errorf(format, a...)
		}
	}
	hostSet, timeoutSet, fieldsSet, linesSet, pathSet := false, false, false, false, false
	var seen []string
	var tuningFlags, poolFlags []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		flag, value, assigned := strings.Cut(arg, "=")
		switch flag {
		case "--host", "--fields", "--timeout", "--lines", "--frequency", "--core-voltage", "--url", "--port", "--user", "--fallback-url", "--fallback-port", "--fallback-user", "--path":
			if flag == "--host" && hostSet {
				fail("--host was given more than once; a command takes one miner")
			}
			if slices.Contains(tuningFlags, flag) || slices.Contains(poolFlags, flag) {
				fail("%s was given more than once", flag)
			}
			if isTuningFlag(flag) {
				tuningFlags = append(tuningFlags, flag)
			}
			poolField, isPoolFlag := poolFlagField(flag)
			if isPoolFlag {
				poolFlags = append(poolFlags, flag)
			}
			if flag == "--path" && pathSet {
				fail("--path was given more than once")
			}
			hostSet = hostSet || flag == "--host"
			pathSet = pathSet || flag == "--path"
			seen = append(seen, flag)
			timeoutSet = timeoutSet || flag == "--timeout"
			fieldsSet = fieldsSet || flag == "--fields"
			linesSet = linesSet || flag == "--lines"
			if !assigned {
				if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
					fail("%s requires a value", flag)
					continue
				}
				i++
				value = args[i]
			}
			if strings.TrimSpace(value) == "" {
				fail("%s requires a non-empty value", flag)
				continue
			}
			if isPoolFlag {
				parsed, accepted := poolFlagValue(poolField, value)
				if !accepted {
					fail("%s requires %s", flag, poolValueRules[poolField])
					continue
				}
				opts.pool[flag] = parsed
				continue
			}
			switch flag {
			case "--path":
				opts.path = value
			case "--host":
				opts.host = value
			case "--timeout":
				seconds, err := strconv.Atoi(value)
				if err != nil || seconds < 1 || seconds > maxDiscoverTimeout {
					fail("--timeout requires whole seconds from 1 to %d", maxDiscoverTimeout)
					continue
				}
				opts.timeout = seconds
			case "--frequency", "--core-voltage":
				number, err := strconv.Atoi(value)
				if err != nil || number < 1 {
					fail("%s requires a whole number from 1", flag)
					continue
				}
				opts.tuning[flag] = number
			case "--lines":
				opts.lines = allLogLines
				if value != "all" {
					count, err := strconv.Atoi(value)
					if err != nil || count < 1 {
						fail("--lines requires a whole number from 1, or all")
						continue
					}
					opts.lines = count
				}
			default:
				opts.fields = strings.Split(value, ",")
				seen := map[string]bool{}
				for j, field := range opts.fields {
					field = strings.TrimSpace(field)
					if field == "" || seen[field] {
						fail("--fields requires unique non-empty comma-separated field names")
						break
					}
					seen[field] = true
					opts.fields[j] = field
				}
			}
		case "--help", "-v", "-V", "--version", "--confirm", "--show-user", "--show-private":
			if assigned {
				fail("%s does not accept a value", flag)
				continue
			}
			if flag != "--help" && flag != "-v" && flag != "-V" && flag != "--version" {
				seen = append(seen, flag)
			}
			switch flag {
			case "--help":
				opts.help = true
			case "--confirm":
				opts.confirm = true
			case "--show-user":
				opts.showUser = true
			case "--show-private":
				opts.showPrivate = true
			default:
				opts.version = true
			}
		default:
			if strings.HasPrefix(arg, "-") {
				fail("unknown flag %s", flag)
				continue
			}
			if opts.command == "skill" && opts.action == "" && arg == "install" {
				opts.action = arg
				continue
			}
			if opts.command == "skill" && opts.action == "" && !strings.HasPrefix(arg, "-") && !slices.Contains(commandNames, arg) {
				fail("unknown action %s for `skill`; valid action: install", arg)
				continue
			}
			if opts.command != "" || !slices.Contains(commandNames, arg) {
				fail("unknown command or argument %s; valid commands: %s", arg, commands())
				continue
			}
			opts.command = arg
		}
	}
	if opts.command == "skill" {
		for _, flag := range seen {
			if flag != "--path" {
				return opts, errors.New("unknown flag " + flag + " for `skill`; it takes --path only")
			}
		}
		if first == nil && opts.action == "" && !opts.help && !opts.version {
			return opts, errors.New(skillUsage)
		}
		if first == nil && opts.action == "" && pathSet {
			return opts, errors.New("--path is valid only with `skill install`")
		}
		return opts, first
	}
	if pathSet {
		return opts, errors.New("unknown flag --path; it is a flag of `skill install` only")
	}
	if opts.command == "discover" && hostSet {
		return opts, errors.New("unknown flag --host for `discover`; it browses the local network")
	}
	if opts.command != "discover" && timeoutSet {
		return opts, errors.New("unknown flag --timeout; it is a flag of `discover` only")
	}
	if opts.command != "logs" && linesSet {
		return opts, errors.New("unknown flag --lines; it is a flag of `logs` only")
	}
	if opts.command != "logs" && opts.showPrivate {
		return opts, errors.New("unknown flag --show-private; it is a flag of `logs` only")
	}
	if opts.command == "logs" && opts.showPrivate && !linesSet && !opts.help && !opts.version {
		return opts, errors.New("--show-private is valid only together with --lines; without --lines the command prints no log line")
	}
	if opts.command == "logs" && fieldsSet {
		return opts, errors.New("unknown flag --fields for `logs`; it prints whole log lines")
	}
	writes := opts.command == "restart" || opts.command == "tuning" || opts.command == "pool"
	if !writes && opts.confirm {
		return opts, errors.New("unknown flag --confirm; it is a flag of `restart`, `tuning` and `pool` only")
	}
	if writes && fieldsSet {
		return opts, errors.New("unknown flag --fields for `" + opts.command + "`; it prints a fixed result")
	}
	if opts.command != "tuning" && len(tuningFlags) != 0 {
		return opts, errors.New("unknown flag " + tuningFlags[0] + "; it is a flag of `tuning` only")
	}
	if first == nil && opts.command == "tuning" && len(tuningFlags) == 0 && !opts.help && !opts.version {
		return opts, errors.New(tuningUsage)
	}
	if opts.command != "pool" && len(poolFlags) != 0 {
		return opts, errors.New("unknown flag " + poolFlags[0] + "; it is a flag of `pool` only")
	}
	if opts.command != "pool" && opts.showUser {
		return opts, errors.New("unknown flag --show-user; it is a flag of `pool` only")
	}
	if first == nil && opts.command == "pool" && len(poolFlags) == 0 && !opts.help && !opts.version {
		return opts, errors.New(poolUsage)
	}
	if first == nil && opts.confirm && !opts.showUser && !opts.help && !opts.version {
		for _, flag := range poolFlags {
			if field, _ := poolFlagField(flag); field == "stratumUser" {
				return opts, errors.New(flag + poolUserUsage)
			}
		}
	}
	return opts, first
}

func write(w io.Writer, fields output.Object) int {
	if err := output.Write(w, fields); err != nil {
		return 1
	}
	return 0
}

func failure(w io.Writer, exit int, code, message, help string) int {
	return failureAfter(w, nil, exit, code, message, help)
}

func failureAfter(w io.Writer, lead output.Object, exit int, code, message, help string) int {
	if write(w, append(lead, output.Object{{Name: "error", Value: output.Object{{Name: "code", Value: code}, {Name: "message", Value: message}}}, {Name: "help", Value: help}}...)) != 0 {
		return 1
	}
	return exit
}

func optionalReadFailure(w io.Writer, err error, subject, host string) int {
	if errors.Is(err, axeos.ErrNotFound) || errors.Is(err, axeos.ErrRootRedirect) {
		return failure(w, 1, "not_supported", subject+" is not supported by this firmware", "axeos-axi info --host "+shellQuote(host)+" shows the firmware version")
	}
	return failure(w, 1, "miner_read_failed", err.Error(), "check --host or AXEOS_HOST and local network connectivity")
}

func (a *App) Run(ctx context.Context, args []string, stdout io.Writer) int {
	opts, err := parse(args)
	if err != nil {
		return failure(stdout, 2, "usage", err.Error(), "valid flags: "+validFlags(opts.command)+"; commands: "+commands())
	}
	if opts.version {
		if _, err := fmt.Fprintln(stdout, a.Version); err != nil {
			return 1
		}
		return 0
	}
	if opts.help {
		return write(stdout, help(opts.command))
	}
	if opts.command == "skill" {
		return installSkill(opts, stdout)
	}
	if opts.command == "discover" {
		return a.discover(ctx, opts, stdout)
	}
	if opts.host == "" {
		opts.host = a.getenv("AXEOS_HOST")
	}
	if opts.host == "" {
		var lead output.Object
		if opts.command == "" {
			lead = identity()
		}
		return failureAfter(stdout, lead, 2, "host_required", "set --host <address> or AXEOS_HOST", "axeos-axi discover finds miners on the local network; then axeos-axi --host <address>")
	}
	client, err := axeos.New(opts.host)
	if err != nil {
		return failure(stdout, 2, "invalid_host", err.Error(), "axeos-axi --host 192.0.2.10")
	}
	if opts.command == "scoreboard" {
		return scoreboard(ctx, client, opts, stdout)
	}
	if opts.command == "logs" {
		return logs(ctx, client, opts, stdout)
	}
	if opts.command == "restart" {
		return restart(ctx, client, opts, stdout)
	}
	if opts.command == "tuning" {
		return tuning(ctx, client, opts, stdout)
	}
	if opts.command == "pool" {
		return pool(ctx, client, opts, stdout)
	}
	var info map[string]any
	if opts.command != "firmware" {
		info, err = client.Get(ctx, "info")
		if err != nil {
			return failure(stdout, 1, "miner_read_failed", err.Error(), "check --host or AXEOS_HOST and local network connectivity")
		}
	}
	var fields output.Object
	raw := info
	switch opts.command {
	case "firmware":
		checksum, readErr := client.Get(ctx, "firmware/checksum")
		if readErr != nil {
			return optionalReadFailure(stdout, readErr, "firmware checksum", opts.host)
		}
		raw = checksum
		fields = firmwareView(checksum)
	case "info":
		fields = infoView(info)
	case "asic":
		asic, readErr := client.Get(ctx, "asic")
		if readErr != nil {
			return failure(stdout, 1, "miner_read_failed", readErr.Error(), "check the miner API with axeos-axi info")
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
		stats, readErr := client.Get(ctx, "statistics")
		if readErr != nil {
			return failure(stdout, 1, "miner_read_failed", readErr.Error(), "check the miner API with axeos-axi info")
		}
		raw = stats
		fields, err = statsView(info, stats)
		if err != nil {
			return failure(stdout, 1, "invalid_statistics", err.Error(), "check AxeOS statistics API compatibility")
		}
	default:
		fields = homeView(info)
	}
	if len(opts.fields) != 0 {
		selected := make(output.Object, 0, len(opts.fields))
		available := map[string]any{}
		for _, name := range strings.Split(viewNames(opts.command), ",") {
			available[name] = nil
		}
		if readsInfoFields(opts.command) {
			for _, name := range conditionalInfoFields {
				available[name] = nil
			}
		}
		for k, v := range raw {
			available[k] = v
		}
		for _, f := range fields {
			available[f.Name] = f.Value
		}
		for _, name := range opts.fields {
			value, ok := available[name]
			if !ok {
				return failure(stdout, 2, "unknown_field", "unknown field "+name, strings.TrimSpace("axeos-axi "+opts.command)+" --help; valid fields: "+viewNames(opts.command)+" (or exact API field names)")
			}
			selected = append(selected, output.Field{Name: name, Value: value})
		}
		if opts.command == "stats" && available["state"] != nil {
			stateSelected := false
			for _, name := range opts.fields {
				stateSelected = stateSelected || name == "state"
			}
			if !stateSelected {
				selected = append(output.Object{{Name: "state", Value: available["state"]}}, selected...)
			}
		}
		fields = selected
	}
	if opts.command == "" {
		fields = append(identity(), fields...)
	}
	if opts.command == "" && len(opts.fields) == 0 {
		// Carry the selected host into commands so --host-only invocations remain actionable.
		hostArg := shellQuote(opts.host)
		fields = append(fields, output.Field{Name: "help", Value: []any{
			"axeos-axi info --host " + hostArg + " for system and network detail",
			"axeos-axi asic --host " + hostArg + " for hardware and tuning",
			"axeos-axi stats --host " + hostArg + " for recorded statistics",
			"axeos-axi firmware --host " + hostArg + " for the running firmware checksum",
			"axeos-axi scoreboard --host " + hostArg + " for the best-difficulty shares",
			"axeos-axi logs --host " + hostArg + " for the log line count and size",
			"axeos-axi discover to find AxeOS miners on the local network",
			"axeos-axi restart --host " + hostArg + " for a preview of a miner restart; it sends no request without --confirm",
			"axeos-axi tuning --host " + hostArg + " --frequency <MHz> --core-voltage <mV> for a preview of a tuning change; it sends no write request without --confirm",
			"axeos-axi pool --host " + hostArg + " --url <host> --port <port> --user <user> for a preview of a pool change; it sends no write request without --confirm",
			"axeos-axi skill install to install the agent skill for this tool",
		}})
	}
	return write(stdout, fields)
}

func identity() output.Object {
	bin, err := os.Executable()
	if err != nil {
		bin = "axeos-axi"
	}
	return output.Object{{Name: "bin", Value: tildePath(bin)}, {Name: "description", Value: "Read and operate an AxeOS Bitcoin miner from a predictable command line"}}
}

func fieldsHelp(command string) string {
	text := "--fields <name,...>; default compact view; replaces data fields; accepts view fields and exact API field names"
	if readsInfoFields(command) {
		text += "; " + conditionalFieldsHelp
	}
	return text
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func help(command string) output.Object {
	if command == "skill" {
		return skillHelp()
	}
	if command == "discover" {
		return discoverHelp()
	}
	if command == "logs" {
		return logsHelp()
	}
	if command == "restart" {
		return restartHelp()
	}
	if command == "tuning" {
		return tuningHelp()
	}
	if command == "pool" {
		return poolHelp()
	}
	label := command
	if label == "" {
		label = "home"
	}
	descriptions := map[string]string{"home": "Live mining health", "info": "System and network detail", "asic": "ASIC hardware and current tuning", "stats": "Recorded sample count and latest sample; disabled logging is an explicit empty state", "firmware": "Running firmware image and its SHA-256, comparable to sha256sum of the release esp-miner.bin; needs firmware newer than v2.15.3, and v2.15.3 and older answer not_supported", "scoreboard": "Best-difficulty shares, highest first, at most 20; default columns " + strings.Join(scoreboardDefaults, ",") + "; --fields replaces the row columns; ntime is the block-header time in Unix seconds"}
	prefix := strings.TrimSpace("axeos-axi " + command)
	fields := output.Object{{Name: "command", Value: label}, {Name: "description", Value: descriptions[label]}}
	if command == "" {
		fields = append(fields, output.Field{Name: "commands", Value: commands() + "; axeos-axi <command> --help; discover finds miners without --host; restart, tuning and pool change the miner and send no write request without --confirm; skill install writes the agent skill file and needs no host"})
	}
	flags := output.Object{
		{Name: "flags", Value: output.Object{
			{Name: "host", Value: hostFlagHelp},
			{Name: "fields", Value: fieldsHelp(command)},
			{Name: "help", Value: "--help; no network request"},
			{Name: "version", Value: "-v, -V, --version; bare version; no network request"},
		}},
		{Name: "timeout_s", Value: 4},
		{Name: "view_fields", Value: viewNames(command)},
	}
	if readsInfoFields(command) {
		flags = append(flags, output.Field{Name: "conditional_fields", Value: strings.Join(conditionalInfoFields, ",")})
	}
	return append(fields, append(flags, output.Object{
		{Name: "private_fields", Value: "info/home/asic: stratumUser,fallbackStratumUser,pools,ssid,macAddr are explicit opt-ins"},
		{Name: "examples", Value: []any{prefix + " --host 192.0.2.10", prefix + " --host 192.0.2.10 --fields " + exampleFields(command), prefix + " --help"}},
	}...)...)
}
