package app

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/Azd325/axeos-axi/internal/axeos"
	"github.com/Azd325/axeos-axi/internal/output"
)

const (
	restartRequest = "POST /api/system/restart"
	restartEffect  = "the miner restarts and stops hashing until it is up again"
)

func restart(ctx context.Context, client *axeos.Client, opts options, stdout io.Writer) int {
	hostArg := shellQuote(opts.host)
	fields := output.Object{{Name: "host", Value: opts.host}, {Name: "request", Value: restartRequest}}
	if !opts.confirm {
		return write(stdout, append(fields,
			output.Field{Name: "sent", Value: false},
			output.Field{Name: "effect", Value: restartEffect},
			output.Field{Name: "execute", Value: "axeos-axi restart --host " + hostArg + " --confirm"},
		))
	}
	uptime := "axeos-axi info --host " + hostArg + " shows uptime_s and reset_reason"
	err := client.Post(ctx, "restart")
	switch {
	case errors.Is(err, axeos.ErrNotSent):
		return failure(stdout, 1, "restart_not_sent", err.Error(), "check --host or AXEOS_HOST and local network connectivity")
	case errors.Is(err, axeos.ErrNoAnswer):
		return failure(stdout, 1, "restart_unconfirmed", err.Error()+"; the restart is unconfirmed", uptime+"; read them before another restart")
	case err != nil:
		return failure(stdout, 1, "restart_failed", err.Error()+"; the miner did not confirm a restart", uptime+" and the firmware version")
	}
	return write(stdout, append(fields,
		output.Field{Name: "sent", Value: true},
		output.Field{Name: "result", Value: "the miner accepted the restart; it stops hashing until it is up again"},
		output.Field{Name: "help", Value: []any{uptime + " when the miner is up again"}},
	))
}

func restartHelp() output.Object {
	return output.Object{
		{Name: "command", Value: "restart"},
		{Name: "description", Value: "Changes the miner: " + restartEffect + "; without --confirm sends no request and prints the host, the request, the effect and the command that performs it; with --confirm sends exactly one " + restartRequest + ", with no read before or after it; HTTP 200 is success; any other outcome is an error that states whether the request was sent"},
		{Name: "flags", Value: output.Object{
			{Name: "host", Value: "--host <address>; default AXEOS_HOST; required; one miner; HTTP unless a scheme is supplied"},
			{Name: "confirm", Value: "--confirm; sends the restart request; default sends no request"},
			{Name: "help", Value: "--help; no network request"},
			{Name: "version", Value: "-v, -V, --version; bare version; no network request"},
		}},
		{Name: "timeout_s", Value: int(axeos.Timeout / time.Second)},
		{Name: "examples", Value: []any{"axeos-axi restart --host 192.0.2.10", "axeos-axi restart --host 192.0.2.10 --confirm", "axeos-axi restart --help"}},
	}
}
