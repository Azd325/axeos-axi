package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Azd325/axeos-axi/internal/axeos"
	"github.com/Azd325/axeos-axi/internal/output"
	"github.com/Azd325/axeos-axi/internal/ws"
)

const maxFollowSeconds = 300

var followUnit = time.Second

const (
	endTimeLimit = "time_limit"
	endClosed    = "closed_by_miner"
	endInterrupt = "interrupted"
)

type logStream struct {
	stdout  io.Writer
	redact  *redactor
	pending string
	count   int
	failed  bool
}

func (s *logStream) accept(text string) {
	s.pending += text
	for {
		line, rest, found := strings.Cut(s.pending, "\n")
		if !found {
			break
		}
		s.pending = rest
		s.emit(line)
	}
	if len(s.pending) > ws.MaxMessage {
		s.emit(s.pending)
		s.pending = ""
	}
}

func (s *logStream) flush() {
	if s.pending != "" {
		s.emit(s.pending)
		s.pending = ""
	}
}

func (s *logStream) emit(line string) {
	if line = printable(line); strings.TrimSpace(line) == "" {
		return
	}
	if s.redact != nil {
		line = s.redact.replace(line)
	}
	s.count++
	if write(s.stdout, output.Object{{Name: fmt.Sprintf("line_%d", s.count), Value: line}}) != 0 {
		s.failed = true
	}
}

func follow(ctx context.Context, client *axeos.Client, opts options, stdout io.Writer) int {
	var redact *redactor
	if !opts.showPrivate {
		info, err := client.Get(ctx, "info")
		if err != nil {
			return failure(stdout, 1, "miner_read_failed", err.Error(), "check --host or AXEOS_HOST and local network connectivity")
		}
		redact = newRedactor(info)
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	conn, err := client.OpenLogStream(ctx)
	interrupted := err != nil && ctx.Err() != nil
	if err != nil && !interrupted {
		return followOpenFailure(stdout, err, opts.host)
	}
	if conn != nil {
		defer func() { _ = conn.Close() }()
	}

	limit := time.Duration(opts.follow) * followUnit
	start := time.Now()
	deadline := start.Add(limit)
	if write(stdout, output.Object{{Name: "follow_limit_s", Value: opts.follow}}) != 0 {
		return 1
	}
	stream := &logStream{stdout: stdout, redact: redact}
	var ended string
	if interrupted {
		ended = endInterrupt
	}
	var readErr error
	for ended == "" && !stream.failed {
		text, err := conn.ReadMessage(ctx, deadline)
		switch {
		case err == nil:
			stream.accept(text)
		case ctx.Err() != nil:
			ended = endInterrupt
		case errors.Is(err, os.ErrDeadlineExceeded):
			ended = endTimeLimit
		case errors.Is(err, ws.ErrClosed):
			ended = endClosed
		default:
			readErr = err
			ended = "error"
		}
	}
	stream.flush()
	if stream.failed {
		return 1
	}
	followed := time.Since(start).Round(100 * time.Millisecond).Seconds()
	prefix := "axeos-axi logs --host " + shellQuote(opts.host)
	summary := output.Object{{Name: "lines", Value: stream.count}, {Name: "seconds_followed", Value: followed}}
	if readErr != nil {
		code, help := "connection_lost", "the miner closed the connection without a close frame, for example after a restart; "+prefix+" --follow "+fmt.Sprint(opts.follow)+" starts a new follow"
		if errors.Is(readErr, ws.ErrProtocol) || errors.Is(readErr, ws.ErrFrameTooLarge) {
			code, help = "protocol_error", "the miner sent data that is not a valid log stream; "+prefix+" --lines <n> reads the log buffer instead"
		}
		return failureAfter(stdout, summary, 1, code, readErr.Error(), help)
	}
	summary = append(summary, output.Field{Name: "ended", Value: ended})
	if stream.count == 0 {
		summary = append(summary, output.Field{Name: "state", Value: "0 new log lines arrived while following"})
	}
	hints := []any{prefix + " --lines <n> for the lines that are already in the log buffer"}
	if redact != nil {
		summary = append(summary, output.Field{Name: "private", Value: logRedacted})
		hints = append(hints, prefix+" --follow "+fmt.Sprint(opts.follow)+" --show-private prints the lines unchanged")
	}
	return write(stdout, append(summary, output.Field{Name: "help", Value: hints}))
}

func followOpenFailure(w io.Writer, err error, host string) int {
	prefix := "axeos-axi logs --host " + shellQuote(host)
	switch {
	case errors.Is(err, axeos.ErrNotFound), errors.Is(err, axeos.ErrRootRedirect):
		return failure(w, 1, "not_supported", "the log stream is not supported by this firmware", "axeos-axi info --host "+shellQuote(host)+" shows the firmware version; "+prefix+" --lines <n> reads the log buffer instead")
	case errors.Is(err, axeos.ErrStreamFull):
		return failure(w, 1, "connections_full", err.Error()+"; the web interface shares these places", "close other log streams or AxeOS web pages, then run "+prefix+" --follow <seconds> again")
	case errors.Is(err, axeos.ErrAccess):
		return failure(w, 1, "access_refused", err.Error(), "run the command from a host in the private network of the miner")
	case errors.Is(err, axeos.ErrHandshake):
		return failure(w, 1, "handshake_failed", err.Error(), prefix+" --lines <n> reads the log buffer instead")
	}
	return failure(w, 1, "miner_read_failed", err.Error(), "check --host or AXEOS_HOST and local network connectivity")
}
