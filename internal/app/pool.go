package app

import (
	"context"
	"errors"
	"io"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Azd325/axeos-axi/internal/axeos"
	"github.com/Azd325/axeos-axi/internal/output"
)

const (
	poolRequest = "PATCH /api/system"
	// The firmware stores a pool record and loads it at once (update_pool_nvs in ESP-Miner main/http_server/http_server.c);
	// the stratum task reads it only when it opens a connection (stratum_v1_task in main/tasks/stratum_v1_task.c).
	poolEffect       = "the miner stores the values at once and keeps them across a restart; an open pool connection uses the old values until the miner restarts or connects again"
	poolUsage        = "pool requires one or more of --url, --port, --user, --fallback-url, --fallback-port and --fallback-user"
	poolNotPrinted   = "<not printed>"
	maxPoolTextBytes = 255
)

type poolSetting struct {
	flag, view, field, label string
}

type poolRole struct {
	name, index string
	settings    []poolSetting
}

var poolRoles = []poolRole{
	{"primary", "primaryPoolIndex", []poolSetting{
		{"--url", "url", "stratumURL", "URL"},
		{"--port", "port", "stratumPort", "port"},
		{"--user", "user", "stratumUser", "user"},
	}},
	{"fallback", "secondaryPoolIndex", []poolSetting{
		{"--fallback-url", "fallback_url", "stratumURL", "URL"},
		{"--fallback-port", "fallback_port", "stratumPort", "port"},
		{"--fallback-user", "fallback_user", "stratumUser", "user"},
	}},
}

var poolValueRules = map[string]string{
	"stratumURL":  "a host name or an address of 1 to 255 bytes, without a scheme, a port or a space",
	"stratumPort": "a whole number from 1 to 65535",
	"stratumUser": "a value of 1 to 255 bytes",
}

func poolFlagField(flag string) (string, bool) {
	for _, role := range poolRoles {
		for _, s := range role.settings {
			if s.flag == flag {
				return s.field, true
			}
		}
	}
	return "", false
}

// The firmware accepts each text of 1 to 255 bytes and each port from 1 to 65535 (validate_pool_json in ESP-Miner
// main/http_server/http_server.c). It also stores a URL with a scheme or a port, which cannot connect.
func poolValue(field string, v any) (known, accepted bool) {
	if field == "stratumPort" {
		n, ok := v.(float64)
		return ok, ok && n == math.Trunc(n) && n >= 1 && n <= 65535
	}
	s, ok := v.(string)
	accepted = ok && s != "" && len(s) <= maxPoolTextBytes
	if field == "stratumURL" {
		accepted = accepted && !strings.Contains(s, "://") && strings.Count(s, ":") != 1 && !strings.ContainsFunc(s, unicode.IsSpace)
	}
	return ok, accepted
}

func poolFlagValue(field, value string) (any, bool) {
	var v any = value
	if field == "stratumPort" {
		n, err := strconv.Atoi(value)
		if err != nil {
			return nil, false
		}
		v = float64(n)
	}
	_, accepted := poolValue(field, v)
	return v, accepted
}

func poolRecord(pools []any, id float64) map[string]any {
	for _, item := range pools {
		if record, ok := item.(map[string]any); ok && record["id"] == id {
			return record
		}
	}
	return nil
}

func poolArgument(v any) string {
	if n, ok := v.(float64); ok {
		return formatNumber(n)
	}
	return shellQuote(v.(string))
}

func printedPool(record map[string]any, showUser bool) map[string]any {
	printed := maps.Clone(record)
	if !showUser {
		printed["stratumUser"] = poolNotPrinted
	}
	if cert, ok := printed["stratumCert"].(string); ok && cert != "" {
		printed["stratumCert"] = poolNotPrinted
	}
	return printed
}

func pool(ctx context.Context, client *axeos.Client, opts options, stdout io.Writer) int {
	hostArg := shellQuote(opts.host)
	command := "axeos-axi pool --host " + hostArg
	configuration := "axeos-axi info --host " + hostArg + " --fields primaryPoolIndex,secondaryPoolIndex,pools shows the pool configuration, with the pool users"
	info, err := client.Get(ctx, "info")
	if err != nil {
		return failure(stdout, 1, "miner_read_failed", err.Error(), "check --host or AXEOS_HOST and local network connectivity")
	}
	listed, isList := info["pools"].([]any)
	primary, primaryKnown := number(info, "primaryPoolIndex")
	fallback, fallbackKnown := number(info, "secondaryPoolIndex")
	if !isList || !primaryKnown || !fallbackKnown {
		return failure(stdout, 1, "not_supported", "the miner reports no pools list with a primary and a fallback index; a pool change is not supported for this firmware", "axeos-axi info --host "+hostArg+" shows the firmware version")
	}
	if primary == fallback {
		return failure(stdout, 1, "same_slot", "the primary and the fallback pool are the same slot "+formatNumber(primary)+", so a change of one changes the other; the write is refused", configuration)
	}
	var records []map[string]any
	var printed, rows []any
	execute, revert := command, command
	hiddenUserChanges := false
	for _, role := range poolRoles {
		if !slices.ContainsFunc(role.settings, func(s poolSetting) bool { _, named := opts.pool[s.flag]; return named }) {
			continue
		}
		slot, _ := number(info, role.index)
		present := poolRecord(listed, slot)
		if present == nil {
			return failure(stdout, 1, "no_pool_in_slot", "the miner reports no pool in slot "+formatNumber(slot)+", the "+role.name+" pool; this tool cannot remove a pool again, so the write is refused", configuration)
		}
		next := maps.Clone(present)
		next["stratumPassword"] = axeos.KeepPassword
		changed := false
		for _, s := range role.settings {
			old := present[s.field]
			known, accepted := poolValue(s.field, old)
			if !known {
				return failure(stdout, 1, "present_value_unknown", "the miner reports no present "+s.label+" for the "+role.name+" pool; the write is refused", configuration)
			}
			if !accepted {
				return failure(stdout, 1, "not_reversible", "the present "+s.label+" of the "+role.name+" pool is not "+poolValueRules[s.field]+", so this tool cannot set it again; the write is refused", configuration)
			}
			value, named := opts.pool[s.flag]
			if !named {
				continue
			}
			next[s.field] = value
			changes := old != value
			changed = changed || changes
			presentCell, newCell, argument, previous := old, value, poolArgument(value), poolArgument(old)
			if s.field == "stratumUser" && !opts.showUser {
				presentCell, newCell, argument, previous = "set", "set", "<user>", "<previous user>"
				hiddenUserChanges = hiddenUserChanges || changes
			}
			rows = append(rows, output.Object{{Name: "setting", Value: s.view}, {Name: "present", Value: presentCell}, {Name: "new", Value: newCell}, {Name: "changes", Value: changes}})
			execute += " " + s.flag + " " + argument
			revert += " " + s.flag + " " + previous
		}
		if changed {
			records = append(records, next)
			printed = append(printed, printedPool(next, opts.showUser))
		}
	}
	if len(records) == 0 {
		return write(stdout, output.Object{
			{Name: "host", Value: opts.host},
			{Name: "sent", Value: false},
			{Name: "settings", Value: rows},
			{Name: "result", Value: "each new value equals the present value; the command sends no write request"},
		})
	}
	fields := output.Object{
		{Name: "host", Value: opts.host},
		{Name: "request", Value: poolRequest},
		{Name: "body", Value: output.Object{{Name: "pools", Value: printed}}},
		{Name: "sent", Value: opts.confirm},
		{Name: "settings", Value: rows},
	}
	if opts.showUser {
		execute += " --show-user"
		revert += " --show-user"
	} else {
		fields = append(fields, output.Field{Name: "private", Value: "the pool user is not printed; --show-user prints it"})
	}
	if !opts.confirm {
		fields = append(fields, output.Field{Name: "effect", Value: poolEffect}, output.Field{Name: "execute", Value: execute + " --confirm"})
		if hiddenUserChanges {
			fields = append(fields, output.Field{Name: "help", Value: []any{"add --show-user to this preview and record the present pool user before --confirm; the miner does not report it after the change"}})
		}
		return write(stdout, fields)
	}
	verify := "axeos-axi info --host " + hostArg + " --fields stratumURL,stratumPort,fallbackStratumURL,fallbackStratumPort shows the stored URL and port of each pool; stratumUser and fallbackStratumUser show the users"
	err = client.PatchPools(ctx, records)
	switch {
	case errors.Is(err, axeos.ErrNotSent):
		return failure(stdout, 1, "pool_not_sent", err.Error(), "check --host or AXEOS_HOST and local network connectivity")
	case errors.Is(err, axeos.ErrNoAnswer):
		return failure(stdout, 1, "pool_unconfirmed", err.Error()+"; the change is unconfirmed", verify+"; read them before another write")
	case err != nil:
		return failure(stdout, 1, "pool_failed", err.Error()+"; the miner did not confirm the change", verify)
	}
	return write(stdout, append(fields,
		output.Field{Name: "result", Value: "the miner accepted the request; " + poolEffect},
		output.Field{Name: "help", Value: []any{
			verify,
			"axeos-axi restart --host " + hostArg + " for a preview of the restart that makes the miner use the stored values",
			revert + " --confirm sets the previous values again",
		}},
	))
}

func poolHelp() output.Object {
	return output.Object{
		{Name: "command", Value: "pool"},
		{Name: "description", Value: "Changes the miner: sets the URL, the port or the user of the primary pool and of the fallback pool; " + poolEffect + "; each call reads the present pool configuration with GET /api/system/info; without --confirm sends no write request and prints the present value and the new value of each named setting and the command that performs the change; with --confirm sends exactly one " + poolRequest + "; the firmware replaces the whole record of a pool, so the body carries the complete record that was read, with the named settings replaced and with the password value that keeps the stored password; a pool with no changed value is not in the body, and when no value changes the command sends no write request; the command cannot set a password and does not restart the miner"},
		{Name: "flags", Value: output.Object{
			{Name: "host", Value: "--host <address>; default AXEOS_HOST; required; one miner; HTTP unless a scheme is supplied"},
			{Name: "url", Value: "--url <host>; primary pool; " + poolValueRules["stratumURL"]},
			{Name: "port", Value: "--port <port>; primary pool; " + poolValueRules["stratumPort"]},
			{Name: "user", Value: "--user <user>; primary pool; " + poolValueRules["stratumUser"]},
			{Name: "fallback_url", Value: "--fallback-url <host>; fallback pool; same rule as --url"},
			{Name: "fallback_port", Value: "--fallback-port <port>; fallback pool; same rule as --port"},
			{Name: "fallback_user", Value: "--fallback-user <user>; fallback pool; same rule as --user"},
			{Name: "show_user", Value: "--show-user; prints each pool user; default prints the word set in place of a pool user"},
			{Name: "confirm", Value: "--confirm; sends the write request; default sends no write request"},
			{Name: "help", Value: "--help; no network request"},
			{Name: "version", Value: "-v, -V, --version; bare version; no network request"},
		}},
		{Name: "timeout_s", Value: int(axeos.Timeout / time.Second)},
		{Name: "private_fields", Value: "the pool user prints only with --show-user; the pool certificate and the password are never printed"},
		{Name: "examples", Value: []any{"axeos-axi pool --host 192.0.2.10 --url pool.example.org --port 3333", "axeos-axi pool --host 192.0.2.10 --fallback-user example-worker --show-user --confirm", "axeos-axi pool --help"}},
	}
}
