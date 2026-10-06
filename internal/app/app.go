package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Azd325/axeos-axi/internal/axeos"
	"github.com/Azd325/axeos-axi/internal/mdns"
	"github.com/Azd325/axeos-axi/internal/output"
)

const commonFlags = "--fields, --help, -v, -V, --version"

var commandNames = []string{"info", "asic", "stats", "discover"}

func commands() string { return strings.Join(commandNames, ", ") }

func validFlags(command string) string {
	if command == "discover" {
		return "--timeout, " + commonFlags
	}
	return "--host, " + commonFlags
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
	command, host string
	timeout       int
	fields        []string
	help, version bool
}

// The command may follow its flags, so parsing continues after the first error
// to report the valid flags of the right command.
func parse(args []string) (options, error) {
	opts := options{timeout: defaultDiscoverTimeout}
	var first error
	fail := func(format string, a ...any) {
		if first == nil {
			first = fmt.Errorf(format, a...)
		}
	}
	hostSet, timeoutSet := false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		flag, value, assigned := strings.Cut(arg, "=")
		switch flag {
		case "--host", "--fields", "--timeout":
			hostSet = hostSet || flag == "--host"
			timeoutSet = timeoutSet || flag == "--timeout"
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
			switch flag {
			case "--host":
				opts.host = value
			case "--timeout":
				seconds, err := strconv.Atoi(value)
				if err != nil || seconds < 1 || seconds > maxDiscoverTimeout {
					fail("--timeout requires whole seconds from 1 to %d", maxDiscoverTimeout)
					continue
				}
				opts.timeout = seconds
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
		case "--help", "-v", "-V", "--version":
			if assigned {
				fail("%s does not accept a value", flag)
				continue
			}
			opts.help = opts.help || flag == "--help"
			opts.version = opts.version || flag != "--help"
		default:
			if strings.HasPrefix(arg, "-") {
				fail("unknown flag %s", flag)
				continue
			}
			if opts.command != "" || !slices.Contains(commandNames, arg) {
				fail("unknown command or argument %s; valid commands: %s", arg, commands())
				continue
			}
			opts.command = arg
		}
	}
	if opts.command == "discover" && hostSet {
		return opts, errors.New("unknown flag --host for `discover`; it browses the local network")
	}
	if opts.command != "discover" && timeoutSet {
		return opts, errors.New("unknown flag --timeout; it is a flag of `discover` only")
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
	if write(w, output.Object{{Name: "error", Value: output.Object{{Name: "code", Value: code}, {Name: "message", Value: message}}}, {Name: "help", Value: help}}) != 0 {
		return 1
	}
	return exit
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
	if opts.command == "discover" {
		return a.discover(ctx, opts, stdout)
	}
	if opts.host == "" {
		opts.host = a.getenv("AXEOS_HOST")
	}
	if opts.host == "" {
		return failure(stdout, 2, "host_required", "set --host <address> or AXEOS_HOST", "axeos-axi discover finds miners on the local network; then axeos-axi --host <address>")
	}
	client, err := axeos.New(opts.host)
	if err != nil {
		return failure(stdout, 2, "invalid_host", err.Error(), "axeos-axi --host 192.0.2.10")
	}
	info, err := client.Get(ctx, "info")
	if err != nil {
		return failure(stdout, 1, "miner_read_failed", err.Error(), "check --host or AXEOS_HOST and local network connectivity")
	}
	var fields output.Object
	raw := info
	switch opts.command {
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
		for k, v := range raw {
			available[k] = v
		}
		for _, f := range fields {
			available[f.Name] = f.Value
		}
		for _, name := range opts.fields {
			value, ok := available[name]
			if !ok {
				return failure(stdout, 2, "unknown_field", "unknown field "+name, strings.TrimSpace("axeos-axi "+opts.command)+" --help; use the documented view fields or exact API field names")
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
		bin, err := os.Executable()
		if err != nil {
			bin = "axeos-axi"
		}
		if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(bin, home+string(filepath.Separator)) {
			bin = "~" + strings.TrimPrefix(bin, home)
		}
		fields = append(output.Object{{Name: "bin", Value: bin}, {Name: "description", Value: "Read an AxeOS Bitcoin miner from a predictable command line"}}, fields...)
		// Carry the selected host into commands so --host-only invocations remain actionable.
		hostArg := shellQuote(opts.host)
		fields = append(fields, output.Field{Name: "help", Value: []any{
			"axeos-axi info --host " + hostArg + " for system and network detail",
			"axeos-axi asic --host " + hostArg + " for hardware and tuning",
			"axeos-axi stats --host " + hostArg + " for recorded statistics",
			"axeos-axi discover to find AxeOS miners on the local network",
		}})
	}
	return write(stdout, fields)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func help(command string) output.Object {
	if command == "discover" {
		return discoverHelp()
	}
	label := command
	if label == "" {
		label = "home"
	}
	descriptions := map[string]string{"home": "Live mining health", "info": "System and network detail", "asic": "ASIC hardware and current tuning", "stats": "Recorded sample count and latest sample; disabled logging is an explicit empty state"}
	prefix := strings.TrimSpace("axeos-axi " + command)
	fields := output.Object{{Name: "command", Value: label}, {Name: "description", Value: descriptions[label]}}
	if command == "" {
		fields = append(fields, output.Field{Name: "commands", Value: commands() + "; axeos-axi <command> --help; discover finds miners without --host"})
	}
	return append(fields, output.Object{
		{Name: "flags", Value: output.Object{
			{Name: "host", Value: "--host <address>; default AXEOS_HOST; required for reads; HTTP unless a scheme is supplied"},
			{Name: "fields", Value: "--fields <name,...>; default compact view; replaces data fields; accepts view fields and exact API field names"},
			{Name: "help", Value: "--help; no network request"},
			{Name: "version", Value: "-v, -V, --version; bare version; no network request"},
		}},
		{Name: "timeout_s", Value: 4},
		{Name: "view_fields", Value: viewNames(command)},
		{Name: "private_fields", Value: "info/home/asic: stratumUser,fallbackStratumUser,pools,ssid,macAddr are explicit opt-ins"},
		{Name: "examples", Value: []any{prefix + " --host 192.0.2.10", prefix + " --host 192.0.2.10 --fields " + exampleFields(command), prefix + " --help"}},
	}...)
}
