package app

import (
	"context"
	"io"
	"slices"
	"strings"

	"github.com/Azd325/axeos-axi/internal/axeos"
	"github.com/Azd325/axeos-axi/internal/output"
)

var (
	scoreboardFields   = []string{"rank", "difficulty", "ntime", "job_id", "extranonce2", "nonce", "version_bits"}
	scoreboardDefaults = scoreboardFields[:3]
)

func scoreboard(ctx context.Context, client *axeos.Client, opts options, stdout io.Writer) int {
	columns := scoreboardDefaults
	if len(opts.fields) != 0 {
		columns = opts.fields
	}
	for _, name := range columns {
		if !slices.Contains(scoreboardFields, name) {
			return failure(stdout, 2, "unknown_field", "unknown field "+name, "axeos-axi scoreboard --help; valid fields: "+strings.Join(scoreboardFields, ","))
		}
	}
	entries, err := client.GetList(ctx, "scoreboard")
	if err != nil {
		return optionalReadFailure(stdout, err, "scoreboard", opts.host)
	}
	rows := make([]map[string]any, len(entries))
	for i, entry := range entries {
		share, ok := entry.(map[string]any)
		if !ok {
			return failure(stdout, 1, "invalid_scoreboard", "scoreboard response contains an entry that is not an object", "check AxeOS scoreboard API compatibility")
		}
		rows[i] = share
	}
	fields := output.Object{{Name: "count", Value: len(rows)}}
	if len(rows) == 0 {
		return write(stdout, append(fields, output.Field{Name: "state", Value: "0 shares recorded on the miner scoreboard"}))
	}
	shares := make([]any, len(rows))
	for i, row := range rows {
		share := make(output.Object, len(columns))
		for j, name := range columns {
			share[j] = output.Field{Name: name, Value: row[name]}
			if name == "rank" {
				share[j].Value = i + 1
			}
		}
		shares[i] = share
	}
	fields = append(fields, output.Field{Name: "shares", Value: shares})
	if len(opts.fields) == 0 {
		fields = append(fields, output.Field{Name: "help", Value: []any{"axeos-axi scoreboard --host " + shellQuote(opts.host) + " --fields " + strings.Join(scoreboardFields, ",") + " for the share proof fields"}})
	}
	return write(stdout, fields)
}
