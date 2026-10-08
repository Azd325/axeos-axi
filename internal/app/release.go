package app

import (
	"context"
	"errors"
	"io"

	"github.com/Azd325/axeos-axi/internal/axeos"
	"github.com/Azd325/axeos-axi/internal/output"
	"github.com/Azd325/axeos-axi/internal/release"
)

const checkReleaseHelp = "--check-release; `firmware` only; one miner; reads info for the firmware version (field version) and sends exactly one HTTPS GET to " + release.Latest + ", the only request of this tool that leaves the local network; the request has no token, no cookie and no value from the miner, and its User-Agent is axeos-axi and its version; no redirect is followed; prints miner_version, release (tag, name, date, url) and comparison; the newest release is the one GitHub marks as latest, so pre-releases are ignored; the comparison is made only when the miner version and the release tag both have the exact form vMAJOR.MINOR.PATCH, numerically by major, minor and patch, and is up_to_date, update_available or newer_than_release; every other form prints comparison: unknown with a reason; sends exactly one request to the miner, so it works also when the miner has no checksum path (v2.15.3 and older), and prints a help line for the plain `firmware` command, which prints the checksum; a failed release read is release_read_failed with exit code 1 and still prints miner_version; downloads nothing; cannot combine with --fields or with more than one --host"

func (a *App) checkRelease(ctx context.Context, client *axeos.Client, opts options, stdout io.Writer) int {
	info, err := client.Get(ctx, "info")
	if err != nil {
		return failure(stdout, 1, "miner_read_failed", err.Error(), "check --host or AXEOS_HOST and local network connectivity")
	}
	minerVersion := info["version"]
	lead := output.Object{{Name: "miner_version", Value: minerVersion}}

	latest, err := release.New(a.releaseURL, a.Version).Latest(ctx)
	if err != nil {
		return failureAfter(stdout, lead, 1, "release_read_failed", err.Error(), releaseFailureHelp(err, opts.hosts[0]))
	}

	version, _ := minerVersion.(string)
	comparison, reason := release.Compare(version, latest.Tag)
	if _, ok := minerVersion.(string); !ok {
		comparison, reason = release.Unknown, "the miner did not report a firmware version"
	}
	fields := append(lead, output.Field{Name: "release", Value: output.Object{
		{Name: "tag", Value: latest.Tag}, {Name: "name", Value: nullIfEmpty(latest.Name)},
		{Name: "date", Value: nullIfEmpty(latest.Date)}, {Name: "url", Value: nullIfEmpty(latest.URL)},
	}}, output.Field{Name: "comparison", Value: comparison})
	if reason != "" {
		fields = append(fields, output.Field{Name: "reason", Value: reason})
	}
	fields = append(fields, output.Field{Name: "help", Value: "axeos-axi firmware --host " + shellQuote(opts.hosts[0]) + " for the running firmware checksum"})
	return write(stdout, fields)
}

func releaseFailureHelp(err error, host string) string {
	switch {
	case errors.Is(err, release.ErrRateLimit):
		return "wait and run axeos-axi firmware --host " + shellQuote(host) + " --check-release again; GitHub limits requests without a token per hour"
	case errors.Is(err, release.ErrRefused):
		return "GitHub refused the request; check the network path to api.github.com, then run axeos-axi firmware --host " + shellQuote(host) + " --check-release again; axeos-axi firmware --host " + shellQuote(host) + " needs no outside request"
	case errors.Is(err, release.ErrUnreachable):
		return "check that this computer reaches api.github.com, then run axeos-axi firmware --host " + shellQuote(host) + " --check-release again; axeos-axi firmware --host " + shellQuote(host) + " needs no outside request"
	}
	return "run axeos-axi firmware --host " + shellQuote(host) + " --check-release again later; axeos-axi firmware --host " + shellQuote(host) + " needs no outside request"
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
