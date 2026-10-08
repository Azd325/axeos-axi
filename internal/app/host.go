package app

import (
	"context"
	"errors"
	"io"
	"slices"
	"time"

	"github.com/Azd325/axeos-axi/internal/axeos"
	"github.com/Azd325/axeos-axi/internal/hostfile"
	"github.com/Azd325/axeos-axi/internal/output"
)

const (
	hostUsage     = "usage: axeos-axi host save <address>, axeos-axi host show, or axeos-axi host forget"
	hostSaveUsage = "usage: axeos-axi host save <address>; the address is an argument"
	hostSaveHelp  = "axeos-axi discover finds miners on the local network; then axeos-axi host save <address>"
	hostOrderHelp = "--host <address>; without it AXEOS_HOST, then the saved host (axeos-axi host --help), and the result then prints saved_host; one of the three is required"
)

var hostActions = []string{"save", "show", "forget"}

// savedHostWriter marks a result that used the saved host; write adds the saved_host line to its first result.
type savedHostWriter struct {
	io.Writer
	host   string
	stated bool
}

func (s *savedHostWriter) state(fields output.Object) output.Object {
	if s.stated {
		return fields
	}
	s.stated = true
	at := 0
	for _, f := range identity() {
		if at < len(fields) && fields[at].Name == f.Name {
			at++
		}
	}
	return slices.Insert(slices.Clone(fields), at, output.Field{Name: "saved_host", Value: s.host})
}

func savedHostFailure(w io.Writer, lead output.Object, path string, err error) int {
	return failureAfter(w, lead, 1, "saved_host_invalid", "the saved host file "+tildePath(path)+" "+err.Error()+"; no host is read from it",
		"axeos-axi host forget removes the file; then axeos-axi host save <address>; --host <address> or AXEOS_HOST names the miner without the file")
}

func hostCommand(ctx context.Context, opts options, stdout io.Writer) int {
	path, err := hostfile.Path()
	if err != nil {
		return failure(stdout, 1, "config_directory_unknown", "could not determine the user configuration directory, so there is no place for a saved host", "--host <address> or AXEOS_HOST names the miner without a saved host")
	}
	location := output.Field{Name: "path", Value: tildePath(path)}
	switch opts.action {
	case "save":
		return saveHost(ctx, opts.address, path, location, stdout)
	case "forget":
		removed, err := hostfile.Remove(path)
		if err != nil {
			return failure(stdout, 1, "host_forget_failed", "the saved host file "+tildePath(path)+" could not be removed", "check the file and the permissions of its directory")
		}
		fields := output.Object{{Name: "removed", Value: removed}, location}
		if !removed {
			fields = append(fields, output.Field{Name: "state", Value: "no host was saved; nothing was removed"})
		}
		return write(stdout, fields)
	}
	saved, err := hostfile.Read(path)
	if err != nil {
		return savedHostFailure(stdout, nil, path, err)
	}
	if saved == "" {
		return write(stdout, output.Object{
			{Name: "saved_host", Value: nil}, location,
			{Name: "state", Value: "no host is saved"},
			{Name: "help", Value: []any{hostSaveHelp + " to save a default host"}},
		})
	}
	return write(stdout, output.Object{{Name: "saved_host", Value: saved}, location})
}

func saveHost(ctx context.Context, address, path string, location output.Field, stdout io.Writer) int {
	client, err := axeos.New(address)
	if err != nil {
		return failure(stdout, 2, "invalid_host", err.Error(), "axeos-axi host save 192.0.2.10")
	}
	info, err := client.Get(ctx, "info")
	if err != nil {
		return failure(stdout, 1, "miner_read_failed", err.Error()+"; nothing was saved", "check the address and local network connectivity; "+hostSaveHelp)
	}
	version, hasVersion := info["version"].(string)
	if _, hasModel := info["ASICModel"].(string); !hasVersion || version == "" || !hasModel {
		return failure(stdout, 1, "not_a_miner", "the address answered, but not with an AxeOS info answer: it has no version and ASICModel text; nothing was saved", hostSaveHelp)
	}
	if err := hostfile.Write(path, client.Base()); err != nil {
		return failure(stdout, 1, "host_save_failed", "the saved host file could not be written to "+tildePath(path), "check the directory and its permissions")
	}
	return write(stdout, output.Object{
		{Name: "saved_host", Value: client.Base()}, location,
		{Name: "firmware", Value: version},
		{Name: "help", Value: []any{
			"axeos-axi for the home view of the saved host; --host and AXEOS_HOST come before it",
			"axeos-axi host forget to remove the saved host",
		}},
	})
}

func parseHostCommand(opts options, seen []string, first error) (options, error) {
	if len(seen) != 0 {
		return opts, errors.New("unknown flag " + seen[0] + " for `host`; `host save <address>` takes the address as an argument")
	}
	if first != nil || opts.version {
		return opts, first
	}
	if opts.action == "" && !opts.help {
		return opts, errors.New(hostUsage)
	}
	if opts.action == "save" && opts.address == "" && !opts.help {
		return opts, errors.New(hostSaveUsage)
	}
	return opts, nil
}

func hostHelp() output.Object {
	return output.Object{
		{Name: "command", Value: "host"},
		{Name: "description", Value: "Saves, shows and removes one default host; a command takes its miner from --host, then AXEOS_HOST, then the saved host, and a result that used the saved host prints saved_host with the address; the saved host is one file named host in the directory axeos-axi of the user configuration directory, readable by the user only; it holds the address and nothing else; only `host save` writes it and only `host forget` removes it; `host` alone is a usage error with exit code 2"},
		{Name: "actions", Value: output.Object{
			{Name: "save", Value: "host save <address>; validates the address as --host does, sends exactly one GET /api/system/info to it, and writes the file only when the answer is an AxeOS info answer (it has a version and an ASICModel text); stores the address as scheme and host, with a port that is not the default; prints saved_host, path and firmware (the version the miner reported); the same address again is no error; a failed check writes nothing, keeps a host saved before, and is miner_read_failed or not_a_miner with exit code 1"},
			{Name: "show", Value: "host show; prints saved_host and path; with no file prints saved_host: null and a state line; sends no request"},
			{Name: "forget", Value: "host forget; removes the file and prints removed: true; with no file prints removed: false and a state line, exit code 0; sends no request"},
		}},
		{Name: "invalid_file", Value: "a file that cannot be read, or that does not hold one address in the form `host save` writes, is the error saved_host_invalid with exit code 1; it names the path and `host forget`, and no host is read from the file; --host and AXEOS_HOST still work"},
		{Name: "flags", Value: output.Object{
			{Name: "json", Value: jsonFlagHelp},
			{Name: "help", Value: "--help; no network request"},
			{Name: "version", Value: versionFlagHelp},
		}},
		{Name: "timeout_s", Value: int(axeos.Timeout / time.Second)},
		{Name: "examples", Value: []any{"axeos-axi host save 192.0.2.10", "axeos-axi host show", "axeos-axi host forget"}},
	}
}
