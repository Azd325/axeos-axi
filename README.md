# axeos-axi

An agent-ergonomic Go CLI for AxeOS Bitcoin miners, including Bitaxe.
It reads a miner and has three commands that change it: `restart`, `tuning` and `pool`.
This is an independent tool, not affiliated with the Bitaxe project.
The [vision](VISION.md) records the agreed interface and the rules for accepting a change.

## Build and use

```sh
go build -o bin/axeos-axi ./cmd/axeos-axi
export AXEOS_HOST=192.0.2.10
bin/axeos-axi
bin/axeos-axi info
bin/axeos-axi asic
bin/axeos-axi stats
bin/axeos-axi firmware
bin/axeos-axi scoreboard
bin/axeos-axi logs
bin/axeos-axi discover
bin/axeos-axi restart
bin/axeos-axi tuning --frequency 525 --core-voltage 1150
bin/axeos-axi pool --url pool.example.org --port 3333
```

`--host <address>` overrides `AXEOS_HOST`. There is no configuration file.
A command takes one miner: `--host` given more than once is a usage error with exit code 2, and no request is sent.
Bare addresses use HTTP; explicit HTTP/HTTPS URLs with optional ports are accepted.
A browser address such as `http://192.0.2.10/#/` is accepted; the fragment is dropped.
Each request times out after four seconds; the `logs` request after fifteen. Redirects are refused.
Each request goes directly to the miner; the proxy environment variables (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`) are ignored.
The read commands send only GET requests to `/api/system/info`, `/api/system/asic`,
`/api/system/statistics`, `/api/system/firmware/checksum`, `/api/system/scoreboard` and
`/api/system/logs`, and mDNS queries from `discover`. Three commands change the miner, each only
with `--confirm`: `restart` sends one POST request to `/api/system/restart`, and `tuning` and
`pool` each send one PATCH request to `/api/system`.

| Command | Default view |
| --- | --- |
| no arguments | Hostname, ASIC model, firmware, current/1h/expected hashrate, temperatures, power, efficiency, fan, active pool URL, pool connection/fallback, shares, best difficulty, uptime, overheat and paused flags |
| `info` | System versions, board, heap, Wi-Fi state/signal, uptime, reset reason and active partition |
| `asic` | ASIC model/count/domains and frequency, core voltage, fan mode and temperature target |
| `stats` | Recorded sample count, logging interval and latest sample's hashrate, temperatures and power |
| `firmware` | Running partition, firmware version, image size and SHA-256 of the running image; needs firmware newer than v2.15.3, and v2.15.3 and older answer `not_supported` |
| `scoreboard` | One row per best-difficulty share, highest first: rank, difficulty and block-header time |
| `logs` | Line count and size in bytes of the miner log buffer; log lines only with `--lines` |
| `discover` | One row per miner found on the local network: address for `--host`, mDNS hostname, family and firmware |
| `restart` | Without `--confirm`: the host, the request, the effect and the command that performs the restart; no request is sent. With `--confirm`: the result of the one restart request |
| `tuning` | Without `--confirm`: the host, the request and its body, the present and the new value of each named setting, the effect and the command that performs the change; no write request is sent. With `--confirm`: the result of the one write request |
| `pool` | Without `--confirm`: the host, the request and its body, the present and the new value of each named setting, the effect and the command that performs the change; no write request is sent. With `--confirm`: the result of the one write request. A pool user prints only with `--show-user` |

All normal results, errors and help use [TOON](https://toonformat.dev/reference/spec.html)
on stdout. Exit codes: **0** success, **1** request/protocol/output error, **2** usage error.
`--help` works on every command without contacting a miner. `-v`, `-V` and `--version`
print the bare version. Unknown flags and arguments are rejected.

## Discovery

`discover` browses mDNS for the service type `_axeos._sub._http._tcp.local`, the `_axeos`
subtype that AxeOS registers on its HTTP service. It takes neither `--host` nor `AXEOS_HOST`,
sends only mDNS queries and calls no miner API. It stores nothing: no cache and no default host.
The command always waits the full `--timeout <seconds>` (default 3, whole seconds from 1 to 60).
Zero miners is a definitive result with exit code 0.

```sh
bin/axeos-axi discover
bin/axeos-axi discover --timeout 10 --fields address,hostname,board,asic,asic_count
```

`address` is the IPv4 address, or the `.local` hostname when the miner advertised no address;
a port other than 80 is appended. `family`, `firmware`, `board`, `asic` and `asic_count` come
from the TXT records of the advertisement and are `null` when a miner does not send them.
`port` is the advertised HTTP port. `instance` is the service instance name; it carries a MAC
suffix and is absent from default output. mDNS stays inside one network segment, and firmware
that does not register the `_axeos` subtype is not found.

## Restart

`restart` changes the miner: the miner restarts and stops hashing until it is up again.
It takes one miner, through `--host` or `AXEOS_HOST`, and no `--fields`.

```sh
bin/axeos-axi restart --host 192.0.2.10
bin/axeos-axi restart --host 192.0.2.10 --confirm
```

Without `--confirm` the command sends no request. It prints `sent: false`, the host, the
request, the effect and, as `execute`, the command line that performs the restart.
With `--confirm` it sends exactly one request, `POST /api/system/restart`, without a query or
a body and with no read before or after it. The miner answers before it restarts.
HTTP 200 is success: the command prints `sent: true` and exits with code 0. Firmware v2.13.0
and newer answers JSON and older firmware answers plain text; the command does not read the body.
Each other outcome is an error with exit code 1 that states whether the request was sent:
`restart_not_sent` when no connection to the miner was made, `restart_unconfirmed` when the
miner closed the connection or did not answer in four seconds, and `restart_failed` when the
miner answered a status other than 200, such as 401 for a client outside the allowed
network range. The command does not repeat the request and does not wait for the miner to
come up again; `info` shows `uptime_s` and `reset_reason` after the restart.

## Tuning

`tuning` changes the miner: it sets the ASIC frequency in MHz, the core voltage in mV, or both.
The values are active at once, without a restart, and the miner keeps them across a restart.
The command does not restart the miner. It takes one miner, through `--host` or `AXEOS_HOST`,
and no `--fields`.

```sh
bin/axeos-axi tuning --host 192.0.2.10 --frequency 525
bin/axeos-axi tuning --host 192.0.2.10 --frequency 525 --core-voltage 1150 --confirm
```

`--frequency <MHz>` and `--core-voltage <mV>` take whole numbers. A call with neither flag, or
with one of them given more than once, is a usage error with exit code 2 and sends no request.
Each call first sends two GET requests: `/api/system/info` for the present values and
`/api/system/asic` for the lists of allowed values, `frequencyOptions` and `voltageOptions`.
Without `--confirm` the command sends no write request. It prints `sent: false`, the request,
its `body`, one `settings` row for each named setting with its present value and its new value,
the effect and, as `execute`, the command line that performs the change.
With `--confirm` it sends exactly one request, `PATCH /api/system`. The body carries each
named setting: `frequency`, `coreVoltage` or both. HTTP 200 is success: the
command prints `sent: true` and, in `help`, the command line that sets the previous values again.

Firmware v2.15.3 checks only that each value is a number from 1 to 65535, so the command holds
a value to the list that the miner reports. It has no built-in list. Each refusal is an error with exit
code 1, in the preview and in the confirmed call, and sends no write request:
`value_not_allowed` when a value is not in the list, with the allowed values in the message;
`no_allowed_values` when the miner reports no list for a named setting;
`present_value_unknown` when the miner reports no present value for a named setting; and
`not_reversible` when the present value is not in the list, because the command could not set
that value again. The present value is outside the list after overclock mode stored such a value
or after the firmware reduced the values in an overheat event.
A new value that equals the present value is not an error: its row has `changes: false`, and
the confirmed call sends the one write request with that value.
A failed read is `miner_read_failed`, or `not_supported` for firmware without the `asic` path.
Each failed write states whether the request was sent: `tuning_not_sent`, `tuning_unconfirmed`
when the miner closed the connection or did not answer in four seconds, and `tuning_failed`
when the miner answered a status other than 200, such as 400 for a rejected value or 401 for a
client outside the allowed network range. The command does not repeat the request;
`asic` shows `frequency_mhz` and `core_voltage_set_mv` after the change.

## Pool

`pool` changes the miner: it sets the URL, the port or the user of the primary pool and of the
fallback pool. The miner stores the values at once and keeps them across a restart. An open pool
connection uses the old values until the miner restarts or connects again. The command does not
restart the miner; `restart` does. It takes one miner, through `--host` or `AXEOS_HOST`, and no
`--fields`.

```sh
bin/axeos-axi pool --host 192.0.2.10 --url pool.example.org --port 3333
bin/axeos-axi pool --host 192.0.2.10 --fallback-user example-worker --show-user --confirm
```

| Flag | Pool | Value |
| --- | --- | --- |
| `--url <host>` | primary | Host name or address, 1 to 255 bytes, without a scheme, a port or a space |
| `--port <port>` | primary | Whole number from 1 to 65535 |
| `--user <user>` | primary | 1 to 255 bytes |
| `--fallback-url <host>` | fallback | Same rule as `--url` |
| `--fallback-port <port>` | fallback | Same rule as `--port` |
| `--fallback-user <user>` | fallback | Same rule as `--user` |

A call with none of these flags, with one of them given more than once, or with a value outside
its rule is a usage error with exit code 2 and sends no request. Firmware v2.15.3 also stores a
URL with a scheme such as `stratum+tcp://` or with a port, and the miner cannot connect to it;
the command refuses both, also an address in brackets such as `[2001:db8::10]:3333`.

Firmware v2.15.3 keeps the pools as a list `pools` of at most 8 slots. `primaryPoolIndex` and
`secondaryPoolIndex` name the slot of the primary pool and of the fallback pool. The firmware
replaces the whole record of a slot in one write: a field that the request omits goes back to
its default. The command therefore reads the record and sends it back complete:

1. Each call sends one GET request, to `/api/system/info`.
2. For each pool with a named setting, the command copies the record that the miner reported
   and replaces the named settings.
3. With `--confirm` it sends exactly one request, `PATCH /api/system`, with the body
   `{"pools":[...]}`. The body carries the complete record of each pool with a named setting,
   also when a new value equals the present value.

The values between the read and the write are not locked. When a second client changes a field
of the same pool after the read, the write sets that field back to the value that was read.

Without `--confirm` the command sends no write request. It prints `sent: false`, the request,
its `body`, one `settings` row for each named setting with its present value and its new value,
the effect and, as `execute`, the command line that performs the change. HTTP 200 is success for
the confirmed call: the command prints `sent: true` and, in `help`, the command line that sets
the previous values again. Each pool flag in a printed command line has the form
`--flag=value`, which the command also accepts for a value that starts with `-`.

The pool user can identify the owner. By default a `settings` row for a user prints `set` as the
present and as the new value, with `changes`, and the `body` and `execute` print a placeholder.
`--show-user` prints each user. The miner does not report the previous user after a change. With
`--confirm`, a call that names `--user` or `--fallback-user` without `--show-user` is therefore a
usage error with exit code 2 and sends no request. The `execute` line of a preview that names a
user without `--show-user` has `--show-user`, and `help` says so. With `--show-user` the
confirmed result prints the previous user in the command line that sets it again.
The command cannot set a password and never prints one. The miner reports each password as
`*****`, and a record with that value keeps the stored password, so each write sends it. The
pool certificate is carried in the request and printed as `<not printed>`.

Each refusal is an error with exit code 1, in the preview and in the confirmed call, and sends
no write request:
`not_supported` when the miner reports no `pools` list with both indexes; the write of firmware
without that list is not traced;
`same_slot` when the primary and the fallback pool are the same slot;
`no_pool_in_slot` when the slot of a named pool has no pool, because the command cannot remove
a pool again;
`present_value_unknown` when the miner reports no URL, port or user for a named pool; and
`not_reversible` when the present value of a named setting is outside the rule of its flag,
because the command could not set that value again. A setting that the call does not name goes
back to the miner as read, also when it is outside the rule; the firmware answers 400 for an
empty text, and the command then reports `pool_failed`.
A failed read is `miner_read_failed`. Each failed write states whether the request was sent:
`pool_not_sent`, `pool_unconfirmed` when the miner closed the connection or did not answer in
four seconds, and `pool_failed` when the miner answered a status other than 200, such as 400
for a rejected record or 401 for a client outside the allowed network range. The firmware
checks each record before it stores one. When the request was sent, `help` also has the command
line that sets the previous values again; a user is in it only with `--show-user`. The command
does not repeat the request;
`info --fields stratumURL,stratumPort,fallbackStratumURL,fallbackStratumPort` shows the stored
values after the change.

## Fields, units and privacy

Default views are deliberately small. `--fields` replaces the data fields with a
comma-separated selection, preserving the requested order. Select the view names
listed in command help or exact top-level JSON field names from the
[AxeOS API](https://github.com/bitaxeorg/ESP-Miner/blob/master/main/http_server/openapi.yaml).
`asic` also accepts fields from `info`; `stats` accepts its statistics-response fields;
`firmware` accepts its checksum-response fields;
`scoreboard` accepts only the view names in its help, as row columns;
`discover` accepts only the view names in its help;
`logs`, `restart`, `tuning` and `pool` do not take `--fields`.
Raw API field values retain their API units; normalized view names/values state units.
Missing view values are `null` or `unknown`, never an inferred healthy state.

```sh
bin/axeos-axi --host 192.0.2.10 --fields hashrate,temperature,power_w
bin/axeos-axi info --fields firmware,board,heap_free_bytes
bin/axeos-axi asic --fields frequencyOptions,voltageOptions
bin/axeos-axi stats --fields labels,statistics
bin/axeos-axi scoreboard --fields rank,difficulty,job_id,nonce
bin/axeos-axi info --fields stratumUser,fallbackStratumUser,ssid,macAddr
```

Pool users, Wi-Fi name, MAC, the `pools` configuration list, coinbase outputs and scriptsig
are absent from default output. Explicit raw-field selection can expose private data,
including users inside `pools`; avoid publishing that output.
`logs --lines` prints log lines as the miner wrote them, and they can contain the pool user,
addresses, hostnames and the Wi-Fi name.
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
`firmware` sends one request, to the checksum path only. Its `sha256` is lowercase hex and
matches `sha256sum` of the flashed `esp-miner.bin`. Firmware without that path answers
HTTP 404 (v2.9.0 and newer) or HTTP 302 to `/` (v2.8.0 and older); the command then
reports `not_supported` with exit code 1 and does not follow the redirect.
`scoreboard` sends one request, to the scoreboard path only. The miner keeps at most 20 shares,
sorted by difficulty, and keeps them across restarts. `rank` is the position in that list;
the API does not send it. `ntime` is the block-header time of the share in Unix seconds.
`job_id`, `extranonce2`, `nonce` and `version_bits` are the proof fields of the share; they
identify neither the owner nor the network and appear only through `--fields`.
Zero shares is a definitive result with exit code 0. Firmware without the path gives
`not_supported`, by the same rule as `firmware`.
`logs` sends one request, to the logs path only. The miner answers with plain text: its log
buffer, at most 512 KiB, oldest line first; the buffer survives a soft restart. Without
`--lines` the command prints no log line: it prints `total_lines`, the response size
`size_bytes` and the commands that print lines. `--lines <n>` prints the newest n lines and
`--lines all` prints every line, in the order of the buffer, so the newest line is last;
`total_lines` and `shown_lines` state how many lines exist and how many are printed.
There is no filter, search or follow. Blank lines are not counted. Terminal control sequences, such as the colour codes of the
firmware, are removed; the text is otherwise unchanged. Zero lines is a definitive result
with exit code 0. Firmware without the path gives `not_supported`, by the same rule as `firmware`.

## Development

The layout follows [router-axi](https://github.com/Azd325/router-axi): `cmd/` entry
point, `internal/app` commands, `internal/axeos` API client, `internal/mdns` browser
and `internal/output` TOON.
Tests use sanitized recorded AxeOS v2.15.3 responses served by `httptest` and run
offline with `go test ./...`. The statistics fixture contains three recorded rows.
The firmware checksum, scoreboard and logs fixtures are written from the API schema, not recorded.
`discover` is tested against a fake browser and hand-built mDNS packets.
The output layer keeps ordered view fields and sorts raw API object keys.
API JSON numbers use Go's float64 precision.

```sh
nix develop --no-pure-eval --command check
```

See [CONTRIBUTING.md](CONTRIBUTING.md). Licensed under [MIT](LICENSE).
