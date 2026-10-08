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
	poolEffect         = "the miner stores the values at once and keeps them across a restart; an open pool connection uses the old values until the miner restarts or connects again"
	poolStaleRead      = "firmware v2.15.3 reports the pool values from before this write on the next read; the stored values are the new ones; a second pool call directly after this one sends the old complete record and sets this change back"
	poolStaleFailure   = "firmware v2.15.3 reports the pool values from before a write on the next read; a pool call that uses that read sends the old complete record and sets the change back"
	poolStaleReadHelp  = "firmware v2.15.3 reports the pool values from before a write on the next read, and the stored values are the new ones; a second pool call directly after the first reads those old values, so its preview and its restore command can show the value from before the first write, and it sends the old complete record, so it sets the first change back; the restore command that the first call printed is the correct one"
	poolUsage          = "pool requires one or more of --url, --port, --user, --fallback-url, --fallback-port and --fallback-user"
	poolUserUsage      = " with --confirm requires --show-user; the miner does not report the previous pool user after the change, so the result must print it in the command that sets it again"
	poolNotPrinted     = "<not printed>"
	poolUserNotPrinted = "the pool user is not printed; --show-user prints it"
	poolUserHelp       = "a confirmed change of a pool user needs --show-user, so the execute command has it; replace <user> with the new user"
	poolVerify         = " --fields stratumURL,stratumPort,fallbackStratumURL,fallbackStratumPort"
	maxPoolTextBytes   = 255
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
		accepted = accepted && !strings.Contains(s, "://") && !strings.HasPrefix(s, "[") && strings.Count(s, ":") != 1 && !strings.ContainsFunc(s, unicode.IsSpace)
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

type poolChange struct {
	setting      poolSetting
	present, new any
}

func (c poolChange) hidden(opts options) bool {
	return c.setting.field == "stratumUser" && !opts.showUser
}

func (c poolChange) cells(opts options) (present, next any) {
	if c.hidden(opts) {
		return "set", "set"
	}
	return c.present, c.new
}

type poolPlan struct {
	records []map[string]any
	printed []any
	changes []poolChange
}

func namedPoolSettings(opts options) []poolSetting {
	var named []poolSetting
	for _, role := range poolRoles {
		for _, s := range role.settings {
			if _, ok := opts.pool[s.flag]; ok {
				named = append(named, s)
			}
		}
	}
	return named
}

func poolArguments(opts options) (arguments string, hiddenUser bool) {
	for _, s := range namedPoolSettings(opts) {
		argument := poolArgument(opts.pool[s.flag])
		if s.field == "stratumUser" && !opts.showUser {
			argument, hiddenUser = "<user>", true
		}
		arguments += " " + s.flag + "=" + argument
	}
	if opts.showUser || hiddenUser {
		arguments += " --show-user"
	}
	return arguments, hiddenUser
}

func (p *poolPlan) previousArguments(opts options) string {
	var arguments string
	hiddenUser := false
	for _, c := range p.changes {
		arguments += " " + c.setting.flag + "=" + poolArgument(c.present)
		hiddenUser = hiddenUser || c.hidden(opts)
	}
	if opts.showUser || hiddenUser {
		arguments += " --show-user"
	}
	return arguments
}

func planPool(info map[string]any, opts options, host string) (*poolPlan, *viewFailure) {
	hostArg := shellQuote(host)
	configuration := "axeos-axi info --host " + hostArg + " --fields primaryPoolIndex,secondaryPoolIndex,pools shows the pool configuration, with the pool users"
	refuse := func(code, message, help string) (*poolPlan, *viewFailure) {
		return nil, &viewFailure{exit: 1, code: code, message: message, help: help}
	}
	listed, isList := info["pools"].([]any)
	primary, primaryKnown := number(info, "primaryPoolIndex")
	fallback, fallbackKnown := number(info, "secondaryPoolIndex")
	if !isList || !primaryKnown || !fallbackKnown {
		return refuse("not_supported", "the miner reports no pools list with a primary and a fallback index; a pool change is not supported for this firmware", "axeos-axi info --host "+hostArg+" shows the firmware version")
	}
	if primary == fallback {
		return refuse("same_slot", "the primary and the fallback pool are the same slot "+formatNumber(primary)+", so a change of one changes the other; the write is refused", configuration)
	}
	plan := &poolPlan{}
	for _, role := range poolRoles {
		if !slices.ContainsFunc(role.settings, func(s poolSetting) bool { _, named := opts.pool[s.flag]; return named }) {
			continue
		}
		slot, _ := number(info, role.index)
		present := poolRecord(listed, slot)
		if present == nil {
			return refuse("no_pool_in_slot", "the miner reports no pool in slot "+formatNumber(slot)+", the "+role.name+" pool; this tool cannot remove a pool again, so the write is refused", configuration)
		}
		next := maps.Clone(present)
		next["stratumPassword"] = axeos.KeepPassword
		for _, s := range role.settings {
			old := present[s.field]
			known, accepted := poolValue(s.field, old)
			if !known {
				return refuse("present_value_unknown", "the miner reports no present "+s.label+" for the "+role.name+" pool; the write is refused", configuration)
			}
			value, named := opts.pool[s.flag]
			if !named {
				continue
			}
			if !accepted {
				return refuse("not_reversible", "the present "+s.label+" of the "+role.name+" pool is not "+poolValueRules[s.field]+", so this tool cannot set it again; the write is refused", configuration)
			}
			next[s.field] = value
			plan.changes = append(plan.changes, poolChange{setting: s, present: old, new: value})
		}
		plan.records = append(plan.records, next)
		plan.printed = append(plan.printed, printedPool(next, opts.showUser))
	}
	return plan, nil
}

func pool(ctx context.Context, client *axeos.Client, opts options, stdout io.Writer) int {
	hostArg := shellQuote(opts.host)
	command := "axeos-axi pool --host " + hostArg
	info, err := client.Get(ctx, "info")
	if err != nil {
		return failure(stdout, 1, "miner_read_failed", err.Error(), connectivityHelp)
	}
	plan, refused := planPool(info, opts, opts.host)
	if refused != nil {
		return failure(stdout, refused.exit, refused.code, refused.message, refused.help)
	}
	var rows []any
	for _, c := range plan.changes {
		present, next := c.cells(opts)
		rows = append(rows, output.Object{{Name: "setting", Value: c.setting.view}, {Name: "present", Value: present}, {Name: "new", Value: next}, {Name: "changes", Value: c.present != c.new}})
	}
	arguments, hiddenUser := poolArguments(opts)
	fields := output.Object{
		{Name: "host", Value: opts.host},
		{Name: "request", Value: poolRequest},
		{Name: "body", Value: output.Object{{Name: "pools", Value: plan.printed}}},
		{Name: "sent", Value: opts.confirm},
		{Name: "settings", Value: rows},
	}
	if !opts.showUser {
		fields = append(fields, output.Field{Name: "private", Value: poolUserNotPrinted})
	}
	if !opts.confirm {
		fields = append(fields, output.Field{Name: "effect", Value: poolEffect}, output.Field{Name: "execute", Value: command + arguments + " --confirm"})
		if hiddenUser {
			fields = append(fields, output.Field{Name: "help", Value: []any{poolUserHelp}})
		}
		return write(stdout, fields)
	}
	verify := "axeos-axi info --host " + hostArg + poolVerify + " shows the URL and port of each pool that the miner reports; stratumUser and fallbackStratumUser show the users"
	previous := command + plan.previousArguments(opts) + " --confirm sets the previous values again"
	err = client.PatchPools(ctx, plan.records)
	switch {
	case errors.Is(err, axeos.ErrNotSent):
		return failure(stdout, 1, "pool_not_sent", err.Error(), connectivityHelp)
	case errors.Is(err, axeos.ErrNoAnswer):
		return failure(stdout, 1, "pool_unconfirmed", err.Error()+"; the change is unconfirmed", verify+"; "+poolStaleFailure+"; "+previous)
	case err != nil:
		return failure(stdout, 1, "pool_failed", err.Error()+"; the miner did not confirm the change", verify+"; "+previous)
	}
	return write(stdout, append(fields,
		output.Field{Name: "result", Value: "the miner accepted the request; " + poolEffect},
		output.Field{Name: "note", Value: poolStaleRead},
		output.Field{Name: "help", Value: []any{
			verify,
			"axeos-axi restart --host " + hostArg + " for a preview of the restart that makes the miner use the stored values",
			previous,
		}},
	))
}

func poolHelp() output.Object {
	return output.Object{
		{Name: "command", Value: "pool"},
		{Name: "description", Value: "Changes the miner: sets the URL, the port or the user of the primary pool and of the fallback pool; " + poolEffect + "; each call reads the present pool configuration with GET /api/system/info; without --confirm sends no write request and prints the present value and the new value of each named setting and the command that performs the change; with --confirm sends exactly one " + poolRequest + "; the firmware replaces the whole record of a pool, so the body carries the complete record that was read, with the named settings replaced and with the password value that keeps the stored password; the body carries each pool with a named setting, also when a new value equals the present value; with --confirm, --user and --fallback-user require --show-user, because the miner does not report the previous user after the change; the command cannot set a password and does not restart the miner; several miners in one call have their own result (see several_miners)"},
		{Name: "flags", Value: output.Object{
			{Name: "host", Value: writeHostsFlagHelp},
			{Name: "url", Value: "--url <host>; primary pool; " + poolValueRules["stratumURL"]},
			{Name: "port", Value: "--port <port>; primary pool; " + poolValueRules["stratumPort"]},
			{Name: "user", Value: "--user <user>; primary pool; " + poolValueRules["stratumUser"]},
			{Name: "fallback_url", Value: "--fallback-url <host>; fallback pool; same rule as --url"},
			{Name: "fallback_port", Value: "--fallback-port <port>; fallback pool; same rule as --port"},
			{Name: "fallback_user", Value: "--fallback-user <user>; fallback pool; same rule as --user"},
			{Name: "show_user", Value: "--show-user; prints each pool user; default prints the word set in place of a pool user; required with --confirm when --user or --fallback-user is named"},
			{Name: "confirm", Value: "--confirm; sends the write request; default sends no write request"},
			{Name: "json", Value: jsonFlagHelp},
			{Name: "help", Value: "--help; no network request"},
			{Name: "version", Value: versionFlagHelp},
		}},
		{Name: "stale_read", Value: poolStaleReadHelp},
		{Name: "several_miners", Value: severalWritesHelp("pool")},
		{Name: "timeout_s", Value: int(axeos.Timeout / time.Second)},
		{Name: "private_fields", Value: "the pool user prints only with --show-user; the pool certificate and the password are never printed"},
		{Name: "examples", Value: []any{"axeos-axi pool --host 192.0.2.10 --url pool.example.org --port 3333", "axeos-axi pool --host 192.0.2.10 --fallback-user example-worker --show-user --confirm", "axeos-axi pool --host 192.0.2.10 --host 192.0.2.11 --fallback-port 3334", "axeos-axi pool --help"}},
	}
}
