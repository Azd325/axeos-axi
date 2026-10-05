# axeos-axi

A read-only, agent-ergonomic Go CLI for AxeOS Bitcoin miners, including Bitaxe.
This is an independent tool, not affiliated with the Bitaxe project.
The [vision](VISION.md) records the agreed interface and ten proposals for review.

## Build and use

```sh
go build -o bin/axeos-axi ./cmd/axeos-axi
export AXEOS_HOST=192.0.2.10
bin/axeos-axi
bin/axeos-axi info
bin/axeos-axi asic
bin/axeos-axi stats
```

`--host <address>` overrides `AXEOS_HOST`. There is no configuration file or discovery.
Bare addresses use HTTP; explicit HTTP/HTTPS URLs with optional ports are accepted.
A browser address such as `http://192.0.2.10/#/` is accepted; the fragment is dropped.
Each request times out after four seconds. Redirects are refused.
This version sends only GET requests to `/api/system/info`, `/api/system/asic` and
`/api/system/statistics`; it has no commands that change the miner.

| Command | Default view |
| --- | --- |
| no arguments | Hostname, ASIC model, firmware, current/1h/expected hashrate, temperatures, power, efficiency, fan, active pool URL, pool connection/fallback, shares, best difficulty, uptime, overheat and paused flags |
| `info` | System versions, board, heap, Wi-Fi state/signal, uptime, reset reason and active partition |
| `asic` | ASIC model/count/domains and frequency, core voltage, fan mode and temperature target |
| `stats` | Recorded sample count, logging interval and latest sample's hashrate, temperatures and power |

All normal results, errors and help use [TOON](https://toonformat.dev/reference/spec.html)
on stdout. Exit codes: **0** success, **1** request/protocol/output error, **2** usage error.
`--help` works on every command without contacting a miner. `-v`, `-V` and `--version`
print the bare version. Unknown flags and arguments are rejected.

## Fields, units and privacy

Default views are deliberately small. `--fields` replaces the data fields with a
comma-separated selection, preserving the requested order. Select the view names
listed in command help or exact top-level JSON field names from the
[AxeOS API](https://github.com/bitaxeorg/ESP-Miner/blob/master/main/http_server/openapi.yaml).
`asic` also accepts fields from `info`; `stats` accepts its statistics-response fields.
Raw API field values retain their API units; normalized view names/values state units.
Missing view values are `null` or `unknown`, never an inferred healthy state.

```sh
bin/axeos-axi --host 192.0.2.10 --fields hashrate,temperature,power_w
bin/axeos-axi info --fields firmware,board,heap_free_bytes
bin/axeos-axi asic --fields frequencyOptions,voltageOptions
bin/axeos-axi stats --fields labels,statistics
bin/axeos-axi info --fields stratumUser,fallbackStratumUser,ssid,macAddr
```

Pool users, Wi-Fi name, MAC, the `pools` configuration list, coinbase outputs and scriptsig
are absent from default output. Explicit raw-field selection can expose private data,
including users inside `pools`; avoid publishing that output.
Efficiency is `power_w * 1000 / current_hashrate_ghs` in J/TH; zero/missing hashrate
makes efficiency unknown. Hashrate is GH/s, temperatures are Celsius, tuning voltage
is mV, frequency is MHz and uptime is seconds.
`pool_connection` preserves AxeOS's reported connection information (for example,
`IPv4`); it is not an independently verified pool-health check.
Statistics timestamps are milliseconds since miner boot, not wall-clock dates.
`stats` reports logging disabled definitively when `statsFrequency` is zero, and
reports zero recorded samples when enabled logging has no data. Extra sample history
is available only through explicit field selection; it is not dumped by default.
An empty-state explanation remains present with `--fields`.

## Development

The layout follows [router-axi](https://github.com/Azd325/router-axi): `cmd/` entry
point, `internal/app` commands, `internal/axeos` API client and `internal/output` TOON.
Tests use sanitized recorded AxeOS v2.15.3 responses served by `httptest` and run
offline with `go test ./...`. The statistics fixture contains three recorded rows.
The output layer keeps ordered view fields and sorts raw API object keys.
API JSON numbers use Go's float64 precision.

```sh
nix develop --no-pure-eval --command check
```

See [CONTRIBUTING.md](CONTRIBUTING.md). Licensed under [MIT](LICENSE).
