package app

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Azd325/axeos-axi/internal/mdns"
	"github.com/Azd325/axeos-axi/internal/output"
)

const (
	// Registered by ESP-Miner in components/connect/connect.c as subtype _axeos of _http._tcp.
	discoverService        = "_axeos._sub._http._tcp.local"
	defaultDiscoverTimeout = 3
	maxDiscoverTimeout     = 60
	httpPort               = 80
)

var (
	discoverFields   = []string{"address", "hostname", "family", "firmware", "board", "asic", "asic_count", "port", "instance"}
	discoverDefaults = discoverFields[:4]
)

func advertised(s mdns.Service, key string) any {
	if value, ok := s.Text[key]; ok {
		return value
	}
	return nil
}

func minerRow(s mdns.Service) map[string]any {
	address := s.Host
	if len(s.Addrs) > 0 {
		address = s.Addrs[0].String()
	}
	if s.Port != httpPort {
		address = net.JoinHostPort(address, strconv.Itoa(s.Port))
	}
	asicCount := advertised(s, "asic_count")
	if n, err := strconv.Atoi(s.Text["asic_count"]); err == nil {
		asicCount = float64(n)
	}
	return map[string]any{
		"address":    address,
		"hostname":   s.Host,
		"family":     advertised(s, "family"),
		"firmware":   advertised(s, "fw_version"),
		"board":      advertised(s, "board"),
		"asic":       advertised(s, "asic"),
		"asic_count": asicCount,
		"port":       float64(s.Port),
		"instance":   s.Instance,
	}
}

func (a *App) discover(ctx context.Context, opts options, stdout io.Writer) int {
	columns := discoverDefaults
	if len(opts.fields) != 0 {
		columns = opts.fields
	}
	for _, name := range columns {
		if !slices.Contains(discoverFields, name) {
			return failure(stdout, 2, "unknown_field", "unknown field "+name, "axeos-axi discover --help; valid fields: "+strings.Join(discoverFields, ","))
		}
	}
	services, err := a.Browser.Browse(ctx, discoverService, time.Duration(opts.timeout)*time.Second)
	if err != nil {
		return failure(stdout, 1, "discovery_failed", err.Error(), "check the local network connection and the local-network permission of this terminal, then run axeos-axi discover")
	}
	rows := make([]map[string]any, len(services))
	for i, s := range services {
		rows[i] = minerRow(s)
	}
	slices.SortFunc(rows, func(x, y map[string]any) int {
		return cmp.Or(cmp.Compare(x["hostname"].(string), y["hostname"].(string)), cmp.Compare(x["address"].(string), y["address"].(string)))
	})
	fields := output.Object{
		{Name: "service", Value: discoverService},
		{Name: "timeout_s", Value: opts.timeout},
		{Name: "count", Value: len(rows)},
	}
	if len(rows) == 0 {
		help := []any{"axeos-axi --host <address> reads a miner at a known address; mDNS stays inside one network segment and a miner must advertise this service type"}
		if opts.timeout < maxDiscoverTimeout {
			help = append([]any{fmt.Sprintf("axeos-axi discover --timeout %d to wait longer", min(opts.timeout*3, maxDiscoverTimeout))}, help...)
		}
		return write(stdout, append(fields,
			output.Field{Name: "state", Value: fmt.Sprintf("0 AxeOS miners answered mDNS service type %s within %d s", discoverService, opts.timeout)},
			output.Field{Name: "help", Value: help},
		))
	}
	miners := make([]any, len(rows))
	for i, row := range rows {
		miner := make(output.Object, len(columns))
		for j, name := range columns {
			miner[j] = output.Field{Name: name, Value: row[name]}
		}
		miners[i] = miner
	}
	help := []any{"axeos-axi --host <address> for live mining health of one miner"}
	if len(opts.fields) == 0 {
		help = append(help, "axeos-axi discover --fields address,hostname,board,asic,asic_count for hardware detail")
	}
	return write(stdout, append(fields, output.Field{Name: "miners", Value: miners}, output.Field{Name: "help", Value: help}))
}

func discoverHelp() output.Object {
	return output.Object{
		{Name: "command", Value: "discover"},
		{Name: "description", Value: "Find AxeOS miners on the local network; sends only mDNS queries and contacts no miner API"},
		{Name: "flags", Value: output.Object{
			{Name: "timeout", Value: fmt.Sprintf("--timeout <seconds>; default %d; whole seconds from 1 to %d; the command always waits the full time", defaultDiscoverTimeout, maxDiscoverTimeout)},
			{Name: "fields", Value: "--fields <name,...>; default " + strings.Join(discoverDefaults, ",") + "; replaces the row columns"},
			{Name: "json", Value: jsonFlagHelp},
			{Name: "help", Value: "--help; no network request"},
			{Name: "version", Value: versionFlagHelp},
		}},
		{Name: "service", Value: discoverService},
		{Name: "timeout_s", Value: defaultDiscoverTimeout},
		{Name: "view_fields", Value: strings.Join(discoverFields, ",")},
		{Name: "private_fields", Value: "instance carries a MAC suffix and is an explicit opt-in"},
		{Name: "examples", Value: []any{"axeos-axi discover", "axeos-axi discover --timeout 10", "axeos-axi discover --fields address,hostname,board,asic"}},
	}
}
