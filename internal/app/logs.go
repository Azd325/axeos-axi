package app

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/Azd325/axeos-axi/internal/axeos"
	"github.com/Azd325/axeos-axi/internal/output"
)

const (
	noLogLines  = 0
	allLogLines = -1
	logPrivacy  = "--lines and --follow replace the pool user, the MAC and the Wi-Fi name; only --show-private prints the lines unchanged, and then they can contain the pool user, addresses, hostnames and the Wi-Fi name; other addresses and hostnames are not replaced"
	logRedacted = "the pool user, the MAC and the Wi-Fi name are replaced; --show-private prints the lines unchanged"

	placePoolUser = "<pool-user>"
	placeMAC      = "<mac>"
	placeWifiName = "<wifi-name>"
)

// ECMA-48 sequences: CSI, then OSC and the DCS/SOS/PM/APC strings with their terminator, then any other escape sequence.
var terminalSequence = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[PX^_][^\x1b]*\x1b\\|[ -/]*[0-~])`)

func printable(line string) string {
	line = terminalSequence.ReplaceAllString(line, "")
	return strings.Map(func(r rune) rune {
		if r != '\t' && unicode.IsControl(r) {
			return -1
		}
		return r
	}, line)
}

var macForm = `[0-9A-Fa-f]{2}(?:[:-][0-9A-Fa-f]{2}){5}`

type redactor struct {
	pattern      *regexp.Regexp
	placeholders map[string]string
}

func newRedactor(info map[string]any) *redactor {
	r := &redactor{placeholders: map[string]string{}}
	add := func(v any, placeholder string) {
		if value, ok := v.(string); ok && value != "" {
			r.placeholders[value] = placeholder
		}
	}
	add(info["stratumUser"], placePoolUser)
	add(info["fallbackStratumUser"], placePoolUser)
	if listed, ok := info["pools"].([]any); ok {
		for _, item := range listed {
			if record, ok := item.(map[string]any); ok {
				add(record["stratumUser"], placePoolUser)
			}
		}
	}
	add(info["ssid"], placeWifiName)
	mac, _ := info["macAddr"].(string)
	values := make([]string, 0, len(r.placeholders))
	for value := range r.placeholders {
		values = append(values, value)
	}
	slices.SortFunc(values, func(a, b string) int { return len(b) - len(a) })
	alternatives := make([]string, 0, len(values)+2)
	for _, value := range values {
		alternatives = append(alternatives, regexp.QuoteMeta(value))
	}
	if mac != "" {
		alternatives = append(alternatives, "(?i:"+regexp.QuoteMeta(mac)+")")
	}
	alternatives = append(alternatives, macForm)
	r.pattern = regexp.MustCompile(strings.Join(alternatives, "|"))
	return r
}

func (r *redactor) replace(line string) string {
	return r.pattern.ReplaceAllStringFunc(line, func(match string) string {
		if placeholder, ok := r.placeholders[match]; ok {
			return placeholder
		}
		return placeMAC
	})
}

func logs(ctx context.Context, client *axeos.Client, opts options, stdout io.Writer) int {
	var redact *redactor
	if opts.lines != noLogLines && !opts.showPrivate {
		info, err := client.Get(ctx, "info")
		if err != nil {
			return failure(stdout, 1, "miner_read_failed", err.Error(), "check --host or AXEOS_HOST and local network connectivity")
		}
		redact = newRedactor(info)
	}
	text, err := client.GetText(ctx, "logs")
	if err != nil {
		return optionalReadFailure(stdout, err, "log download", opts.host)
	}
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if line = printable(line); strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	total := len(lines)
	if total == 0 {
		return write(stdout, output.Object{{Name: "total_lines", Value: 0}, {Name: "state", Value: "0 lines in the miner log buffer"}})
	}
	prefix := "axeos-axi logs --host " + shellQuote(opts.host)
	help := output.Field{Name: "help", Value: []any{
		fmt.Sprintf("%s --lines all for all %d lines", prefix, total),
		prefix + " --lines <n> for the newest n lines",
	}}
	if opts.lines == noLogLines {
		return write(stdout, output.Object{{Name: "total_lines", Value: total}, {Name: "size_bytes", Value: len(text)}, help})
	}
	if opts.lines != allLogLines && opts.lines < total {
		lines = lines[total-opts.lines:]
	}
	rows := make([]any, len(lines))
	for i, line := range lines {
		if redact != nil {
			line = redact.replace(line)
		}
		rows[i] = output.Object{{Name: "text", Value: line}}
	}
	fields := output.Object{{Name: "total_lines", Value: total}, {Name: "shown_lines", Value: len(lines)}, {Name: "lines", Value: rows}}
	if redact == nil {
		if len(lines) < total {
			fields = append(fields, help)
		}
		return write(stdout, fields)
	}
	fields = append(fields, output.Field{Name: "private", Value: logRedacted})
	hints := []any{prefix + " --lines " + shown(opts.lines) + " --show-private prints the lines unchanged"}
	if len(lines) < total {
		hints = append(hints, help.Value.([]any)...)
	}
	return write(stdout, append(fields, output.Field{Name: "help", Value: hints}))
}

func shown(lines int) string {
	if lines == allLogLines {
		return "all"
	}
	return fmt.Sprint(lines)
}

func logsHelp() output.Object {
	return output.Object{
		{Name: "command", Value: "logs"},
		{Name: "description", Value: "Line count and size of the miner log buffer; prints no log line without --lines; --lines prints the newest lines, oldest first and newest last, with total_lines and shown_lines; blank lines and terminal control sequences are removed; --lines first reads info from the miner and replaces the pool user, the MAC and the Wi-Fi name by <pool-user>, <mac> and <wifi-name>, and any string in the form of a MAC address by <mac>; a reported value that is a common word is replaced everywhere, so the value can be guessed from the output; --follow <seconds> is a separate mode that reads the log stream /api/ws for at most 300 seconds and prints each new line when it arrives, with the same replacement, as line_1: <text>, line_2: <text> and so on, one TOON field on one output line each, so the output is valid line by line and as a whole; the first output line is follow_limit_s; the last lines are lines, seconds_followed and ended (time_limit, closed_by_miner or interrupted); 0 new lines is a definitive empty state, not an error; the stream sends no old line, so --lines reads the buffer; Ctrl-C ends with exit code 0; a broken connection prints the lines so far, then a connection_lost error with exit code 1; data that is not a valid log stream prints a protocol_error with exit code 1; a failed read of info prints miner_read_failed with exit code 1; a follow holds 1 of the 10 WebSocket places of the miner, which the web interface shares, and the miner answers connections_full when all are taken; firmware older than v2.10.0 has no limit of 10 places, and firmware without the path answers not_supported"},
		{Name: "flags", Value: output.Object{
			{Name: "host", Value: hostFlagHelp},
			{Name: "lines", Value: "--lines <n|all>; default prints no log line; the newest n lines, or all lines"},
			{Name: "follow", Value: "--follow <seconds>; whole seconds from 1 to 300; prints each new log line while it runs, then ends by itself; cannot combine with --lines"},
			{Name: "show_private", Value: "--show-private; only with --lines or --follow; prints the lines unchanged and sends no info request"},
			{Name: "help", Value: "--help; no network request"},
			{Name: "version", Value: "-v, -V, --version; bare version; no network request"},
		}},
		{Name: "timeout_s", Value: int(axeos.LogsTimeout / time.Second)},
		{Name: "follow_max_s", Value: maxFollowSeconds},
		{Name: "privacy", Value: logPrivacy},
		{Name: "examples", Value: []any{"axeos-axi logs --host 192.0.2.10", "axeos-axi logs --host 192.0.2.10 --lines 100", "axeos-axi logs --host 192.0.2.10 --lines all", "axeos-axi logs --host 192.0.2.10 --lines 100 --show-private", "axeos-axi logs --host 192.0.2.10 --follow 30"}},
	}
}
