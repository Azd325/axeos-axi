package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/Azd325/axeos-axi/internal/axeos"
	"github.com/Azd325/axeos-axi/internal/output"
)

const (
	severalRestartEffect = "each miner restarts and stops hashing until it is up again; a restart has no command that reverses it"
	severalWritesRules   = "; a miner that fails the check has the error code in error and null in the value columns; when a miner fails the check, no write request is sent to any miner, and with --confirm each result is not_attempted; else the miners get the write request one after the other, in the order of the flags; the first failed write stops the call: that row is failed with the error code, and each later row is not_attempted; failed counts the rows with an error; exit code 0 only when each result is changed, 1 for any other result and for a preview with a failed miner, 2 for a usage error; the same host twice is a usage error and no request is sent; AXEOS_HOST and the saved host name one miner; no flag takes the hosts from discover"
)

type minerWrite struct {
	values  []any
	changes bool
	refused *viewFailure
	send    func(context.Context) error
	restore any
}

type severalWrite struct {
	request, effect, arguments string
	columns                    []string
	hiddenUser                 bool
	check                      func(context.Context, *axeos.Client, string) minerWrite
	inspect                    func(hostArg string) string
}

func restartSeveral() severalWrite {
	return severalWrite{
		request: restartRequest,
		effect:  severalRestartEffect,
		columns: []string{"uptime_s"},
		check: func(ctx context.Context, client *axeos.Client, _ string) minerWrite {
			info, err := client.Get(ctx, "info")
			if err != nil {
				return minerWrite{refused: readFailed(err, connectivityHelp)}
			}
			return minerWrite{
				values:  []any{info["uptimeSeconds"]},
				changes: true,
				send:    func(ctx context.Context) error { return client.Post(ctx, "restart") },
			}
		},
		inspect: func(hostArg string) string { return "axeos-axi info --host " + hostArg },
	}
}

func poolSeveral(opts options) severalWrite {
	arguments, hiddenUser := poolArguments(opts)
	var columns []string
	for _, s := range namedPoolSettings(opts) {
		columns = append(columns, s.view+"_present", s.view+"_new")
	}
	return severalWrite{
		request:    poolRequest,
		effect:     poolEffect,
		arguments:  arguments,
		columns:    columns,
		hiddenUser: hiddenUser,
		check: func(ctx context.Context, client *axeos.Client, host string) minerWrite {
			info, err := client.Get(ctx, "info")
			if err != nil {
				return minerWrite{refused: readFailed(err, connectivityHelp)}
			}
			plan, refused := planPool(info, opts, host)
			if refused != nil {
				return minerWrite{refused: refused}
			}
			miner := minerWrite{
				send:    func(ctx context.Context) error { return client.PatchPools(ctx, plan.records) },
				restore: "axeos-axi pool --host " + shellQuote(host) + plan.previousArguments(opts) + " --confirm",
			}
			for _, c := range plan.changes {
				present, next := c.cells(opts)
				miner.values = append(miner.values, present, next)
				miner.changes = miner.changes || c.present != c.new
			}
			return miner
		},
		inspect: func(hostArg string) string { return "axeos-axi pool --host " + hostArg + arguments },
	}
}

func writeFailureCode(command string, err error) string {
	switch {
	case errors.Is(err, axeos.ErrNotSent):
		return command + "_not_sent"
	case errors.Is(err, axeos.ErrNoAnswer):
		return command + "_unconfirmed"
	}
	return command + "_failed"
}

func hostArguments(hosts []string) string {
	quoted := make([]string, len(hosts))
	for i, host := range hosts {
		quoted[i] = "--host " + shellQuote(host)
	}
	return strings.Join(quoted, " ")
}

func writeMiners(ctx context.Context, clients []*axeos.Client, opts options, stdout io.Writer) int {
	isPool := opts.command == "pool"
	job := restartSeveral()
	if isPool {
		job = poolSeveral(opts)
	}
	count := len(clients)
	miners := make([]minerWrite, count)
	var wg sync.WaitGroup
	for i, client := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			miners[i] = job.check(ctx, client, opts.hosts[i])
		}()
	}
	wg.Wait()

	codes := make([]any, count)
	results := make([]any, count)
	var help []any
	failed := 0
	for i, m := range miners {
		results[i] = "not_attempted"
		if m.refused == nil {
			continue
		}
		codes[i] = m.refused.code
		failed++
		if failed <= maxFailureHelp {
			help = append(help, job.inspect(shellQuote(opts.hosts[i]))+" for the error message of that miner")
		}
	}
	checkFailed := failed

	sent, changed, stopped := false, 0, -1
	var stopMessage string
	if opts.confirm && checkFailed == 0 {
		for i, m := range miners {
			err := m.send(ctx)
			sent = sent || !errors.Is(err, axeos.ErrNotSent)
			if err != nil {
				results[i], codes[i] = "failed", writeFailureCode(opts.command, err)
				if errors.Is(err, axeos.ErrNotSent) {
					miners[i].restore = nil
				}
				stopped, stopMessage = i, err.Error()
				failed++
				break
			}
			results[i] = "changed"
			changed++
		}
	}

	rows := make([]any, count)
	for i, m := range miners {
		row := make(output.Object, 0, len(job.columns)+4)
		row = append(row, output.Field{Name: "host", Value: opts.hosts[i]})
		for j, name := range job.columns {
			var value any
			if m.refused == nil {
				value = m.values[j]
			}
			row = append(row, output.Field{Name: name, Value: value})
		}
		switch {
		case !opts.confirm:
			var changes any
			if m.refused == nil {
				changes = m.changes
			}
			row = append(row, output.Field{Name: "changes", Value: changes})
		case isPool:
			var restore any
			if results[i] == "changed" || results[i] == "failed" {
				restore = m.restore
			}
			row = append(row, output.Field{Name: "result", Value: results[i]}, output.Field{Name: "restore", Value: restore})
		default:
			row = append(row, output.Field{Name: "result", Value: results[i]})
		}
		rows[i] = append(row, output.Field{Name: "error", Value: codes[i]})
	}

	hosts := hostArguments(opts.hosts)
	fields := output.Object{
		{Name: "request", Value: job.request},
		{Name: "sent", Value: sent},
		{Name: "count", Value: count},
		{Name: "failed", Value: failed},
	}
	if opts.confirm {
		fields = append(fields, output.Field{Name: "changed", Value: changed})
	}
	fields = append(fields, output.Field{Name: "miners", Value: rows})
	if job.hiddenUser {
		fields = append(fields, output.Field{Name: "private", Value: poolUserNotPrinted})
	}

	if !opts.confirm {
		fields = append(fields, output.Field{Name: "effect", Value: job.effect}, output.Field{Name: "execute", Value: "axeos-axi " + opts.command + " " + hosts + job.arguments + " --confirm"})
		if checkFailed != 0 {
			help = append(help, "the execute command sends no write request while a miner fails the check")
		}
		if job.hiddenUser {
			help = append(help, poolUserHelp)
		}
		if len(help) != 0 {
			fields = append(fields, output.Field{Name: "help", Value: help})
		}
		if write(stdout, fields) != 0 || checkFailed != 0 {
			return 1
		}
		return 0
	}

	verify := "axeos-axi info " + hosts + " --fields uptime_s,reset_reason shows uptime_s and reset_reason of each miner"
	if isPool {
		verify = "axeos-axi info " + hosts + poolVerify + " shows the URL and port of each pool of each miner that the miner reports; stratumUser and fallbackStratumUser show the users"
	}
	staleRead := isPool && (changed != 0 || (stopped >= 0 && codes[stopped] == "pool_unconfirmed"))
	var result string
	switch {
	case checkFailed != 0:
		result = fmt.Sprintf("%d of %d miners failed the check before the write, so no write request was sent to any miner", checkFailed, count)
	case stopped >= 0:
		result = fmt.Sprintf("the write to miner %d of %d failed: %s; the call stopped with %d changed before it and %d not attempted after it", stopped+1, count, stopMessage, changed, count-stopped-1)
		switch {
		case staleRead:
			help = append(help, verify+"; "+poolStaleFailure)
		case isPool:
			help = append(help, verify)
		default:
			help = append(help, verify+"; read them before another "+opts.command+" call")
		}
	case !isPool:
		result = "each miner accepted the restart and stops hashing until it is up again; a restart has no command that reverses it"
		help = append(help, verify+" when it is up again")
	default:
		result = "each miner accepted the request; " + poolEffect
		help = append(help, verify, "axeos-axi restart "+hosts+" for a preview of the restart that makes each changed miner use the stored values")
	}
	if isPool && (changed != 0 || (stopped >= 0 && miners[stopped].restore != nil)) {
		help = append(help, "restore has the command that sets the previous values of that one miner again")
	}
	fields = append(fields, output.Field{Name: "result", Value: result})
	if isPool && changed != 0 {
		fields = append(fields, output.Field{Name: "note", Value: poolStaleRead})
	}
	if len(help) != 0 {
		fields = append(fields, output.Field{Name: "help", Value: help})
	}
	if write(stdout, fields) != 0 || failed != 0 {
		return 1
	}
	return 0
}

func severalWritesHelp(command string) string {
	if command == "restart" {
		return "with --host given more than once: each call first sends one GET /api/system/info to each miner, at the same time, as the check; prints request, sent, count, failed and miners, a table with one row per miner in the order of the flags; without --confirm the columns are host, uptime_s, changes and error, no restart request is sent, and execute has the complete command; with --confirm the columns are host, uptime_s, result and error, and the result is changed, failed or not_attempted" +
			severalWritesRules + "; a failed write is restart_not_sent, restart_unconfirmed or restart_failed; a restart has no command that reverses it"
	}
	return "with --host given more than once: each call first sends one GET /api/system/info to each miner, at the same time, as the check; prints request, sent, count, failed and miners, a table with one row per miner in the order of the flags; without --confirm the columns are host, the present and the new value of each named setting (such as port_present and port_new), changes and error, no write request is sent, and execute has the complete command; with --confirm the columns are host, the same values, result, restore and error, and the result is changed, failed or not_attempted; restore is the complete command that sets the previous values of that one miner again, printed for each changed miner and for a failed miner that got the request" +
		severalWritesRules + "; a miner fails the check when its read fails or when a call with one --host refuses the write; a failed write is pool_not_sent, pool_unconfirmed or pool_failed; the body is not printed, and a call with one --host prints it"
}
