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
	"github.com/Azd325/axeos-axi/internal/hostfile"
	"github.com/Azd325/axeos-axi/internal/mdns"
	"github.com/Azd325/axeos-axi/internal/output"
	"github.com/Azd325/axeos-axi/internal/release"
)

const (
	universalFlags  = "--json, --help, -v, -V, --version"
	jsonFlagHelp    = "--json; prints the result as one JSON document with the same fields, values and help lines; errors as one JSON object with code, message and help; the exit code is unchanged"
	versionFlagHelp = "-v, -V, --version; bare version, or with --json one object {\"version\":\"...\"}; no network request"
	hostFlagHelp    = hostOrderHelp + "; one miner; HTTP unless a scheme is supplied"
	hostsFlagHelp   = hostOrderHelp + "; HTTP unless a scheme is supplied; repeat --host to read several miners in one call (see several_miners); AXEOS_HOST and the saved host name one miner"
	severalHelp     = "with --host given more than once: prints count, failed and miners, a table with one row per miner in the order of the flags; the columns are host, the fields of this view or of --fields, and error; a value the miner does not send is null; a miner that fails has its row with the error code (miner_read_failed, not_supported, invalid_statistics) in error and null in the value columns, and the other miners still print; exit code 1 when any miner failed, 2 for a usage error; the miners are read at the same time, each with the timeout below, and each gets the requests of a single-host call; the same host twice is a usage error and no request is sent; accepted by the home view, info, asic, stats without --samples and --columns, and firmware; scoreboard, logs, health, restart, tuning, pool take one miner"
)

var commandNames = []string{"info", "asic", "stats", "firmware", "scoreboard", "logs", "health", "discover", "restart", "tuning", "pool", "host", "skill"}

func commands() string { return strings.Join(commandNames, ", ") }

func validFlags(command string) string {
	switch command {
	case "skill":
		return "--path, " + universalFlags
	case "host":
		return universalFlags
	case "discover":
		return "--timeout, --fields, " + universalFlags
	case "stats":
		return "--host, --fields, --samples, --columns, " + universalFlags
	case "logs":
		return "--host, --lines, --follow, --show-private, " + universalFlags
	case "health":
		return "--host, " + universalFlags
	case "restart":
		return "--host, --confirm, " + universalFlags
	case "firmware":
		return "--host, --fields, --check-release, " + universalFlags
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

	releaseURL string
}

func New(getenv func(string) string) *App {
	return &App{getenv: getenv, Version: "dev", Browser: mdns.Multicast{}}
}

type options struct {
	command, host, action  string
	address                string
	hosts                  []string
	path                   string
	timeout, lines, follow int
	samples                int
	columns                []string
	fields                 []string
	tuning                 map[string]int
	pool                   map[string]any
	help, version, confirm bool
	showUser, showPrivate  bool
	checkRelease           bool
	json                   bool
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
	hostSet, timeoutSet, fieldsSet, linesSet, followSet, pathSet, samplesSet, columnsSet := false, false, false, false, false, false, false, false
	var seen []string
	var tuningFlags, poolFlags []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		flag, value, assigned := strings.Cut(arg, "=")
		switch flag {
		case "--host", "--fields", "--timeout", "--lines", "--follow", "--samples", "--columns", "--frequency", "--core-voltage", "--url", "--port", "--user", "--fallback-url", "--fallback-port", "--fallback-user", "--path":
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
			if (flag == "--samples" && samplesSet) || (flag == "--columns" && columnsSet) || (flag == "--follow" && followSet) {
				fail("%s was given more than once", flag)
			}
			samplesSet = samplesSet || flag == "--samples"
			followSet = followSet || flag == "--follow"
			columnsSet = columnsSet || flag == "--columns"
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
				opts.hosts = append(opts.hosts, value)
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
			case "--follow":
				seconds, err := strconv.Atoi(value)
				if err != nil || seconds < 1 || seconds > maxFollowSeconds {
					fail("--follow requires whole seconds from 1 to %d; %d seconds is the largest time limit", maxFollowSeconds, maxFollowSeconds)
					continue
				}
				opts.follow = seconds
			case "--samples":
				opts.samples = allSamples
				if value != "all" {
					count, err := strconv.Atoi(value)
					if err != nil || count < 1 {
						fail("--samples requires a whole number from 1, or all")
						continue
					}
					opts.samples = count
				}
			case "--columns":
				opts.columns = strings.Split(value, ",")
				seen := map[string]bool{}
				for j, name := range opts.columns {
					name = strings.TrimSpace(name)
					if !slices.Contains(axeos.StatisticsColumns, name) || seen[name] {
						fail("--columns requires unique comma-separated names from: %s", strings.Join(axeos.StatisticsColumns, ","))
						break
					}
					seen[name] = true
					opts.columns[j] = name
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
		case "--help", "-v", "-V", "--version", "--json", "--confirm", "--show-user", "--show-private", "--check-release":
			if assigned {
				opts.json = opts.json || flag == "--json"
				fail("%s does not accept a value", flag)
				continue
			}
			if flag != "--help" && flag != "--json" && flag != "-v" && flag != "-V" && flag != "--version" {
				seen = append(seen, flag)
			}
			switch flag {
			case "--help":
				opts.help = true
			case "--json":
				opts.json = true
			case "--confirm":
				opts.confirm = true
			case "--show-user":
				opts.showUser = true
			case "--show-private":
				opts.showPrivate = true
			case "--check-release":
				opts.checkRelease = true
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
			if opts.command == "skill" && opts.action == "" && !slices.Contains(commandNames, arg) {
				fail("unknown action %s for `skill`; valid action: install", arg)
				continue
			}
			if opts.command == "host" && opts.action == "" && slices.Contains(hostActions, arg) {
				opts.action = arg
				continue
			}
			if opts.command == "host" && opts.action == "save" && opts.address == "" {
				opts.address = arg
				continue
			}
			if opts.command == "host" {
				fail("unknown argument %s for `host`; %s", arg, hostUsage)
				continue
			}
			if opts.command != "" || !slices.Contains(commandNames, arg) {
				fail("unknown command or argument %s; valid commands: %s", arg, commands())
				continue
			}
			opts.command = arg
		}
	}
	if opts.checkRelease && opts.command != "firmware" && first == nil {
		return opts, errors.New("unknown flag --check-release; it is a flag of `firmware` only")
	}
	if len(opts.hosts) > 1 {
		if first != nil {
			return opts, first
		}
		if opts.checkRelease {
			return opts, errors.New("--check-release takes one miner; --host was given more than once, and the comparison for several miners is not available")
		}
		if err := severalHostsError(opts, samplesSet || columnsSet); err != nil {
			return opts, err
		}
	}
	if opts.command == "host" {
		return parseHostCommand(opts, seen, first)
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
	if opts.command != "logs" && followSet {
		return opts, errors.New("unknown flag --follow; it is a flag of `logs` only")
	}
	if opts.command == "logs" && followSet && linesSet {
		return opts, errors.New("--follow cannot be combined with --lines; --follow prints only the lines that arrive while it runs, --lines prints the lines in the log buffer")
	}
	if opts.command != "stats" && (samplesSet || columnsSet) {
		flag := "--samples"
		if !samplesSet {
			flag = "--columns"
		}
		return opts, errors.New("unknown flag " + flag + "; it is a flag of `stats` only")
	}
	if fieldsSet && (samplesSet || columnsSet) {
		return opts, errors.New("--fields cannot be combined with --samples or --columns; they print a table of samples")
	}
	if opts.checkRelease && fieldsSet {
		return opts, errors.New("--fields cannot be combined with --check-release; it prints a fixed result")
	}
	if columnsSet && !samplesSet {
		opts.samples = 1
	}
	if opts.command != "logs" && opts.showPrivate {
		return opts, errors.New("unknown flag --show-private; it is a flag of `logs` only")
	}
	if opts.command == "logs" && opts.showPrivate && !linesSet && !followSet && !opts.help && !opts.version {
		return opts, errors.New("--show-private is valid only together with --lines or --follow; without them the command prints no log line")
	}
	if opts.command == "logs" && fieldsSet {
		return opts, errors.New("unknown flag --fields for `logs`; it prints whole log lines")
	}
	writes := opts.command == "restart" || opts.command == "tuning" || opts.command == "pool"
	if !writes && opts.confirm {
		return opts, errors.New("unknown flag --confirm; it is a flag of `restart`, `tuning` and `pool` only")
	}
	if (writes || opts.command == "health") && fieldsSet {
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
	if saved, ok := w.(*savedHostWriter); ok {
		w, fields = saved.Writer, saved.state(fields)
	}
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
	if opts.json {
		stdout = output.JSON(stdout)
	}
	if err != nil {
		return failure(stdout, 2, "usage", err.Error(), "valid flags: "+validFlags(opts.command)+"; commands: "+commands())
	}
	if opts.version {
		if opts.json {
			return write(stdout, output.Object{{Name: "version", Value: a.Version}})
		}
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
	if opts.command == "host" {
		return hostCommand(ctx, opts, stdout)
	}
	if len(opts.hosts) == 0 {
		if host := a.getenv("AXEOS_HOST"); host != "" {
			opts.hosts = []string{host}
		}
	}
	if len(opts.hosts) == 0 {
		var lead output.Object
		if opts.command == "" {
			lead = identity()
		}
		var saved string
		if path, err := hostfile.Path(); err == nil {
			if saved, err = hostfile.Read(path); err != nil {
				return savedHostFailure(stdout, lead, path, err)
			}
		}
		if saved == "" {
			return failureAfter(stdout, lead, 2, "host_required", "set --host <address> or AXEOS_HOST", "axeos-axi discover finds miners on the local network; then axeos-axi --host <address>; axeos-axi host save <address> saves a default host")
		}
		opts.hosts = []string{saved}
		stdout = &savedHostWriter{Writer: stdout, host: saved}
	}
	clients := make([]*axeos.Client, len(opts.hosts))
	for i, host := range opts.hosts {
		client, err := axeos.New(host)
		if err != nil {
			return failure(stdout, 2, "invalid_host", err.Error(), "axeos-axi --host 192.0.2.10")
		}
		for j := range i {
			if clients[j].Base() == client.Base() {
				return failure(stdout, 2, "usage", "--host names "+client.Base()+" more than once; "+opts.hosts[j]+" and "+host+" are the same miner", "valid flags: "+validFlags(opts.command)+"; commands: "+commands())
			}
		}
		clients[i] = client
	}
	if len(clients) > 1 {
		return readMiners(ctx, clients, opts, stdout)
	}
	opts.host = opts.hosts[0]
	client := clients[0]
	if opts.command == "scoreboard" {
		return scoreboard(ctx, client, opts, stdout)
	}
	if opts.command == "logs" && opts.follow != 0 {
		return follow(ctx, client, opts, stdout)
	}
	if opts.command == "logs" {
		return logs(ctx, client, opts, stdout)
	}
	if opts.command == "health" {
		return health(ctx, client, opts, stdout)
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
	if opts.checkRelease {
		return a.checkRelease(ctx, client, opts, stdout)
	}
	view, readErr := readView(ctx, client, opts.host, opts)
	if readErr != nil {
		return failure(stdout, readErr.exit, readErr.code, readErr.message, readErr.help)
	}
	fields, unknown := view.selected(opts)
	if unknown != "" {
		return failure(stdout, 2, "unknown_field", "unknown field "+unknown, strings.TrimSpace("axeos-axi "+opts.command)+" --help; valid fields: "+viewNames(opts.command)+" (or exact API field names)")
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
	if command == "host" {
		return hostHelp()
	}
	if command == "logs" {
		return logsHelp()
	}
	if command == "health" {
		return healthHelp()
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
	hostHelp := hostFlagHelp
	several := slices.Contains([]string{"", "info", "asic", "stats", "firmware"}, command)
	if several {
		hostHelp = hostsFlagHelp
	}
	descriptions := map[string]string{"home": "Live mining health", "info": "System and network detail", "asic": "ASIC hardware and current tuning", "stats": "Recorded sample count and latest sample; disabled logging is an explicit empty state; --samples and --columns print recorded samples, oldest first and newest last, as a table of the timestamp and the named columns, with sample_count and shown_samples; --columns alone prints the newest sample; --samples alone prints the default columns", "firmware": "Running firmware image and its SHA-256, comparable to sha256sum of the release esp-miner.bin; needs firmware newer than v2.15.3, and v2.15.3 and older answer not_supported; --check-release compares the miner firmware version with the newest GitHub release and works without the checksum path", "scoreboard": "Best-difficulty shares, highest first, at most 20; default columns " + strings.Join(scoreboardDefaults, ",") + "; --fields replaces the row columns; ntime is the block-header time in Unix seconds"}
	prefix := strings.TrimSpace("axeos-axi " + command)
	fields := output.Object{{Name: "command", Value: label}, {Name: "description", Value: descriptions[label]}}
	if command == "" {
		fields = append(fields, output.Field{Name: "commands", Value: commands() + "; axeos-axi <command> --help; discover finds miners without --host; restart, tuning and pool change the miner and send no write request without --confirm; host save stores a default host in one file, and no other command writes that file; skill install writes the agent skill file and needs no host"})
	}
	flags := output.Object{
		{Name: "flags", Value: output.Object{
			{Name: "host", Value: hostHelp},
			{Name: "fields", Value: fieldsHelp(command)},
			{Name: "json", Value: jsonFlagHelp},
			{Name: "help", Value: "--help; no network request"},
			{Name: "version", Value: versionFlagHelp},
		}},
		{Name: "timeout_s", Value: 4},
		{Name: "view_fields", Value: viewNames(command)},
	}
	if several {
		flags = append(flags[:1:1], append(output.Object{{Name: "several_miners", Value: severalHelp}}, flags[1:]...)...)
	}
	if command == "stats" {
		flagList := flags[0].Value.(output.Object)
		flagList = append(flagList[:2:2], append(output.Object{
			{Name: "samples", Value: "--samples <n|all>; the newest n samples, or all samples; without --columns prints " + strings.Join(defaultHistoryColumns, ",") + "; cannot combine with --fields"},
			{Name: "columns", Value: "--columns <name,...>; sends the columns query to the miner and prints these columns and the timestamp; without --samples prints the newest sample; names: " + strings.Join(axeos.StatisticsColumns, ",") + "; firmware older than v2.11.0 ignores the query and answers its own older column names, so most named columns print null there; a column the miner omits prints null; cannot combine with --fields"},
		}, flagList[2:]...)...)
		flags[0].Value = flagList
	}
	if command == "firmware" {
		flagList := flags[0].Value.(output.Object)
		flagList = append(flagList[:2:2], append(output.Object{{Name: "check_release", Value: checkReleaseHelp}}, flagList[2:]...)...)
		flags[0].Value = flagList
		flags = append(flags, output.Field{Name: "release_timeout_s", Value: int(release.Timeout.Seconds())})
	}
	if readsInfoFields(command) {
		flags = append(flags, output.Field{Name: "conditional_fields", Value: strings.Join(conditionalInfoFields, ",")})
	}
	examples := []any{prefix + " --host 192.0.2.10", prefix + " --host 192.0.2.10 --fields " + exampleFields(command), prefix + " --help"}
	if command == "stats" {
		examples[2] = prefix + " --host 192.0.2.10 --samples 10 --columns fanRpm,wifiRssi"
	}
	if several {
		examples = append(examples, prefix+" --host 192.0.2.10 --host 192.0.2.11")
	}
	if command == "firmware" {
		examples = append(examples, prefix+" --host 192.0.2.10 --check-release")
	}
	return append(fields, append(flags, output.Object{
		{Name: "private_fields", Value: "info/home/asic: stratumUser,fallbackStratumUser,pools,ssid,macAddr are explicit opt-ins"},
		{Name: "examples", Value: examples},
	}...)...)
}
