package app

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/Azd325/axeos-axi/internal/axeos"
	"github.com/Azd325/axeos-axi/internal/output"
)

const (
	defaultLogLines = 20
	allLogLines     = 0
	logPrivacy      = "log lines are printed as the miner wrote them and can contain the pool user, addresses, hostnames and the Wi-Fi name"
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

func logs(ctx context.Context, client *axeos.Client, opts options, stdout io.Writer) int {
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
	if opts.lines != allLogLines && opts.lines < total {
		lines = lines[total-opts.lines:]
	}
	rows := make([]any, len(lines))
	for i, line := range lines {
		rows[i] = output.Object{{Name: "text", Value: line}}
	}
	fields := output.Object{{Name: "total_lines", Value: total}, {Name: "shown_lines", Value: len(lines)}, {Name: "lines", Value: rows}}
	if len(lines) < total {
		prefix := "axeos-axi logs --host " + shellQuote(opts.host)
		fields = append(fields, output.Field{Name: "help", Value: []any{
			fmt.Sprintf("%s --lines all for all %d lines", prefix, total),
			prefix + " --lines <n> for the newest n lines",
		}})
	}
	return write(stdout, fields)
}

func logsHelp() output.Object {
	return output.Object{
		{Name: "command", Value: "logs"},
		{Name: "description", Value: fmt.Sprintf("Newest lines of the miner log buffer, oldest first and newest last; default %d lines; total_lines and shown_lines state how many lines exist and how many are printed; blank lines and terminal control sequences are removed", defaultLogLines)},
		{Name: "flags", Value: output.Object{
			{Name: "host", Value: hostFlagHelp},
			{Name: "lines", Value: fmt.Sprintf("--lines <n|all>; default %d; the newest n lines, or all lines", defaultLogLines)},
			{Name: "help", Value: "--help; no network request"},
			{Name: "version", Value: "-v, -V, --version; bare version; no network request"},
		}},
		{Name: "timeout_s", Value: int(axeos.LogsTimeout / time.Second)},
		{Name: "privacy", Value: logPrivacy},
		{Name: "examples", Value: []any{"axeos-axi logs --host 192.0.2.10", "axeos-axi logs --host 192.0.2.10 --lines 100", "axeos-axi logs --host 192.0.2.10 --lines all"}},
	}
}
