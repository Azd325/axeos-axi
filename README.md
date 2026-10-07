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
`/api/system/logs`, and mDNS queries from `discover`. `logs --follow` reads the WebSocket path
`/api/ws`. `logs --lines` and `logs --follow` also read `/api/system/info`
first, to find the private values to replace. Three commands change the miner, each only
with `--confirm`: `restart` sends one POST request to `/api/system/restart`, and `tuning` and
`pool` each send one PATCH request to `/api/system`.

| Command | Default view |
| --- | --- |
| no arguments | Hostname, ASIC model, firmware, current/1h/expected hashrate, temperatures, power, efficiency, fan, active pool URL, pool connection/fallback, shares, best difficulty, uptime, overheat and paused flags |
| `info` | System versions, board, heap, Wi-Fi state/signal, uptime, reset reason and active partition |
| `asic` | ASIC model/count/domains and frequency, core voltage, fan mode and temperature target |
| `stats` | Recorded sample count, logging interval and latest sample's hashrate, temperatures and power; with `--samples` or `--columns`, the newest samples as a table of named columns |
| `firmware` | Running partition, firmware version, image size and SHA-256 of the running image; needs firmware newer than v2.15.3, and v2.15.3 and older answer `not_supported` |
| `scoreboard` | One row per best-difficulty share, highest first: rank, difficulty and block-header time |
| `logs` | Line count and size in bytes of the miner log buffer; log lines only with `--lines`; with `--follow <seconds>`, each new line while it runs; private values replaced unless `--show-private` |
| `discover` | One row per miner found on the local network: address for `--host`, mDNS hostname, family and firmware |
| `restart` | Without `--confirm`: the host, the request, the effect and the command that performs the restart; no request is sent. With `--confirm`: the result of the one restart request |
| `tuning` | Without `--confirm`: the host, the request and its body, the present and the new value of each named setting, the effect and the command that performs the change; no write request is sent. With `--confirm`: the result of the one write request |
| `pool` | Without `--confirm`: the host, the request and its body, the present and the new value of each named setting, the effect and the command that performs the change; no write request is sent. With `--confirm`: the result of the one write request. A pool user prints only with `--show-user` |
| `skill` | `skill install` only: the path of the skill file and whether the command wrote it |

By default, results, errors and help use [TOON](https://toonformat.dev/reference/spec.html)
on stdout. Exit codes: **0** success, **1** request/protocol/output error, **2** usage error.
`--json` on any command prints the same result as one JSON document: the same field names, values,
counts, empty states and help lines, in the same order. A TOON table becomes an array of objects, a
TOON list becomes an array. An error is one object with `error` (`code` and `message`) and `help`, with
the same exit code; a usage error honors `--json` when the flag is in the arguments. `--json` never prints
a value that the TOON output of the same call hides. Without `--json` the output is unchanged.
`--help` works on every command without contacting a miner. `-v`, `-V` and `--version`
print the bare version; with `--json` they print `{"version":"..."}`. Unknown flags and arguments are rejected.

## Agent integrations

`skill install` writes the agent skill that is built into the binary. It needs no host, sends no request and reads no miner.

```sh
axeos-axi skill install
axeos-axi skill install --path ~/.claude/skills
```

The default target is `~/.agents/skills/axeos-axi/SKILL.md`. `--path <directory>` selects another parent directory.
The command prints the absolute path, shown with `~` when it is under the home directory, and whether it wrote the file. The command does not expand a `~` inside `--path`. A repeated install with the same content writes nothing.
A test fails when the help block inside the skill differs from `axeos-axi --help`.

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
`logs`, `restart`, `tuning` and `pool` do not take `--fields`; `stats` does not combine `--fields` with `--samples` or `--columns`.
For `info`, `asic` and the home view, firmware v2.15.3 sends some fields only on a condition,
for example `power_fault` during a fault. `--help` lists them as `conditional_fields`.
With `--fields`, such a name that the miner omits prints `null`.
The home view prints one `help` line for each command; with `--fields` it prints no `help` field.
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
`logs --lines` replaces the pool users, the MAC and the Wi-Fi name that the miner reports by
`<pool-user>`, `<mac>` and `<wifi-name>`, and any string in the form of a MAC address by `<mac>`.
A reported value that is a common word is replaced everywhere, so the value can be guessed from the output.
The replacement also works from the form of the line, whatever the miner reports: every output of a
`Coinbase outputs` listing, the Bitcoin payout address included, becomes `<payout-address>` (a script such as
`OP_RETURN` becomes `<output-script>`), the `Scriptsig` text becomes `<scriptsig>`, and the `params` of a received
`mining.notify` message, the block template with the pool tag and the payout script, become one placeholder that
names it. The user in a sent `mining.authorize` or `mining.submit` message is replaced by `<pool-user>`.
The first line of the log buffer, when it has no log prefix and is not `--- SYSTEM RESTART ---`, is the cut end of a longer line and becomes `<redacted: cut end of a longer line>`.
`--follow` replaces every piece of a line longer than 64 KiB by the same placeholder.
`--lines` or `--follow` with `--show-private` prints log lines as the miner wrote them, and they can contain the
pool user, the payout address, the block template, hostnames and the Wi-Fi name. IP addresses, hostnames and pool
addresses are not replaced.
Efficiency is `power_w * 1000 / current_hashrate_ghs` in J/TH; zero/missing hashrate
makes efficiency unknown. Hashrate is GH/s, temperatures are Celsius, tuning voltage
is mV, frequency is MHz and uptime is seconds.
`pool_connection` preserves AxeOS's reported connection information (for example,
`IPv4`); it is not an independently verified pool-health check.
Statistics timestamps are milliseconds since miner boot, not wall-clock dates.
`stats` reports logging disabled definitively when `statsFrequency` is zero, and
reports zero recorded samples when enabled logging has no data. An empty-state explanation
remains present with `--fields`, `--samples` and `--columns`.
Sample history is not dumped by default. `stats --samples <n|all>` prints the newest n samples,
or all of them, as a table with the timestamp and the columns `hashrate`, `hashrate_1h`, `asicTemp`,
`vrTemp` and `power`, oldest first and newest last, with `sample_count` (all samples the miner holds)
and `shown_samples`. `stats --columns <name,...>` prints the named columns instead; without `--samples`
it prints the newest sample only. A name outside this list is a usage error, and no request is sent:
`hashrate`, `hashrate_1m`, `hashrate_10m`, `hashrate_1h`, `errorPercentage`, `asicTemp`, `asicTemp2`,
`vrTemp`, `asicVoltage`, `voltage`, `power`, `current`, `fanSpeed`, `fanRpm`, `fan2Rpm`, `wifiRssi`,
`freeHeap` and `responseTime`. The `timestamp` column is always printed. Only a call with `--columns`
sends the `columns` query (`GET /api/system/statistics?columns=...`); firmware older than v2.11.0 has
no such query and answers its own older column names (v2.10.1: `hashRate`, `temp`, `vrTemp`, `power`,
`voltage`, `current`, `coreVoltageActual`, `fanspeed`, `fanrpm`, `wifiRSSI`, `freeHeap`, `timestamp`),
so most named columns print `null` there. A column the miner does not send prints `null`. The `help` line for
`--samples all` appears only when the output is cut.

```sh
bin/axeos-axi stats --samples 10
bin/axeos-axi stats --columns fanRpm,wifiRssi
bin/axeos-axi stats --columns fanRpm,wifiRssi --samples all
```
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
`logs` without `--lines`, and `logs --lines` with `--show-private`, send one request, to the logs path only.
`logs --lines` without `--show-private` first sends one request to the info path, then one to the logs path;
if the info read fails, no log line is printed and the exit code is 1.
`--show-private` is valid only together with `--lines` or `--follow`. The miner answers with plain text: its log
buffer, at most 512 KiB, oldest line first; the buffer survives a soft restart. Without
`--lines` the command prints no log line: it prints `total_lines`, the response size
`size_bytes` and the commands that print lines. `--lines <n>` prints the newest n lines and
`--lines all` prints every line, in the order of the buffer, so the newest line is last;
`total_lines` and `shown_lines` state how many lines exist and how many are printed.
There is no filter or search. Blank lines are not counted. Terminal control sequences, such as the colour codes of the
firmware, are removed; with `--show-private` the text is otherwise unchanged. Zero lines is a definitive result
with exit code 0. Firmware without the path gives `not_supported`, by the same rule as `firmware`.

`logs --follow <seconds>` is a separate mode that prints each new log line of the miner while it runs.
The number is a whole number of seconds from 1 to 300 and is the time limit: a value outside this range
is a usage error, and no connection is made. The command ends by itself when the time is over or the
miner closes the connection; Ctrl-C closes the connection cleanly and ends with exit code 0.
`--follow` cannot be combined with `--lines` or `--fields`. `--show-private` works as in `logs --lines`:
without it the pool user, the MAC, the Wi-Fi name, the payout address and the block template are replaced in each line.
The miner sends only the lines that are new after the connection opens, so a follow shows no old line;
`logs --lines` reads the old lines.

With `--json`, the stream is one JSON object per line: `{"follow_limit_s":N}`, then one object for each
log line, such as `{"line_1":"..."}`, then one closing object with `lines`, `seconds_followed`, `ended`
and the other closing fields. A broken connection ends with one object that holds the closing fields and the error.

Without `--json`, the output is a stream of TOON fields, one per output line, so it is valid for a reader that takes it
line by line and for a reader that takes it whole. The first line is `follow_limit_s`. Each log line is
a field `line_1`, `line_2` and so on, printed when it arrives. The last lines are the closing state:
`lines`, `seconds_followed` and `ended`, which is `time_limit`, `closed_by_miner` or `interrupted`.
Zero lines in the time is a definitive result with a `state` line and exit code 0.
A connection that breaks in the middle prints the lines received so far and then a `connection_lost`
error with exit code 1; there is no reconnect. Other errors are `not_supported` (firmware without the
path), `connections_full` (HTTP 429), `access_refused` (HTTP 401), `handshake_failed`,
`protocol_error` (the miner sent data that is not a valid log stream) and `miner_read_failed` (the info
request or the connection failed). All of them end with exit code 1.

A follow holds 1 of the 10 WebSocket places of the miner. The web interface of the miner shares these
places, so a follow can fail with `connections_full` while others are connected. The tool has its own small
WebSocket client in `internal/ws`: it reads text frames, answers a ping and closes cleanly, refuses a frame
larger than 16 KiB and a message larger than 64 KiB, treats a masked frame from the server as a protocol
fault, and has no function that sends a data frame. The firmware has the path since v2.2.0; the limit of
10 places and the HTTP 429 answer exist since v2.10.0.

```sh
bin/axeos-axi logs --follow 30
bin/axeos-axi logs --follow 300 --show-private
```

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
