package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Azd325/axeos-axi/internal/axeos"
	"github.com/Azd325/axeos-axi/internal/output"
)

const wifiScanFixURL = "https://github.com/bitaxeorg/ESP-Miner/pull/2029"

var (
	wifiScanFields   = []string{"rssi", "authmode", "ssid"}
	wifiScanDefaults = wifiScanFields[:2]
)

const wifiScanRisk = "EXPERIMENTAL: with more than 20 access points in range, firmware without the upstream fix " + wifiScanFixURL + " writes past a 20-entry array and can crash and restart the miner, which drops its Wi-Fi and pool connections"

func wifiScan(ctx context.Context, client *axeos.Client, opts options, stdout io.Writer) int {
	if !opts.experimental {
		return failure(stdout, 2, "experimental_required", wifiScanRisk+"; pass --experimental to run the scan anyway", runHint("axeos-axi wifi-scan --host "+shellQuote(opts.host)+" --experimental", "to accept the risk and scan"))
	}
	columns := wifiScanDefaults
	if len(opts.fields) != 0 {
		columns = opts.fields
	}
	for _, name := range columns {
		if !slices.Contains(wifiScanFields, name) {
			return failure(stdout, 2, "unknown_field", "unknown field "+name, runHint("axeos-axi wifi-scan --help", "for the help page")+"; valid fields: "+strings.Join(wifiScanFields, ","))
		}
	}
	entries, err := client.GetWifiScan(ctx)
	if err != nil {
		var status *axeos.StatusError
		if errors.As(err, &status) && status.Code == http.StatusInternalServerError {
			return failure(stdout, 1, "scan_failed", "the miner reported that the Wi-Fi scan failed; it also answers this while another scan runs", runHint("axeos-axi wifi-scan --host "+shellQuote(opts.host)+" --experimental", "to try again later"))
		}
		return optionalReadFailure(stdout, err, "wifi scan", opts.host)
	}
	rows := make([]map[string]any, len(entries))
	for i, entry := range entries {
		network, ok := entry.(map[string]any)
		if !ok {
			return failure(stdout, 1, "invalid_wifi_scan", "wifi scan response contains an entry that is not an object", "check AxeOS wifi scan API compatibility")
		}
		rows[i] = network
	}
	fields := output.Object{{Name: "count", Value: len(rows)}}
	if len(rows) == 0 {
		return write(stdout, append(fields, output.Field{Name: "state", Value: "0 networks found by the miner scan"}))
	}
	networks := make([]any, len(rows))
	for i, row := range rows {
		network := make(output.Object, len(columns))
		for j, name := range columns {
			network[j] = output.Field{Name: name, Value: row[name]}
		}
		networks[i] = network
	}
	fields = append(fields, output.Field{Name: "networks", Value: networks})
	if len(opts.fields) == 0 {
		fields = append(fields, output.Field{Name: "help", Value: []any{runHint("axeos-axi wifi-scan --host "+shellQuote(opts.host)+" --experimental --fields "+strings.Join(wifiScanFields, ","), "for the network names")}})
	}
	return write(stdout, fields)
}

func wifiScanHelp() output.Object {
	return output.Object{
		{Name: "command", Value: "wifi-scan"},
		{Name: "description", Value: "EXPERIMENTAL: the networks the miner sees, at most 20, with count; default columns " + strings.Join(wifiScanDefaults, ",") + "; the network name (ssid) prints only when named in --fields, because it can identify the owner or the neighbourhood; authmode is the wifi_auth_mode_t number from 0 to 12, 0 is open; the scan can take about 10 seconds"},
		{Name: "experimental", Value: wifiScanRisk + "; the scan runs only with --experimental; firmware v2.15.4 and older have no fix; this command can be removed or change without notice"},
		{Name: "flags", Value: output.Object{
			{Name: "host", Value: hostFlagHelp},
			{Name: "experimental", Value: "--experimental; required; accepts the risk that the scan restarts the miner on firmware without the upstream fix"},
			{Name: "fields", Value: "--fields <name,...>; replaces the row columns; names: " + strings.Join(wifiScanFields, ",")},
			{Name: "json", Value: jsonFlagHelp},
			{Name: "help", Value: "--help; no network request"},
			{Name: "version", Value: versionFlagHelp},
		}},
		{Name: "timeout_s", Value: int(axeos.WifiScanTimeout / time.Second)},
		{Name: "view_fields", Value: strings.Join(wifiScanFields, ",")},
		{Name: "examples", Value: []any{"axeos-axi wifi-scan --host 192.0.2.10 --experimental", "axeos-axi wifi-scan --host 192.0.2.10 --experimental --fields ssid,rssi", "axeos-axi wifi-scan --help"}},
	}
}
