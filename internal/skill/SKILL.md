---
name: axeos-axi
description: Read an AxeOS or Bitaxe Bitcoin miner (health verdict, ASIC, statistics, firmware, shares, logs, discovery) and restart it, tune frequency and core voltage, or set its pools with confirmation, through the axeos-axi CLI. Use when an agent works with a miner on the local network.
---

# axeos-axi

`axeos-axi` is a non-interactive CLI for AxeOS miners, including Bitaxe.
It prints compact TOON on stdout; `--json` on any command prints the same result as JSON (`logs --follow --json` prints one JSON object per line). This skill is static text; run the CLI for live miner state.

## Usage

- Name the miner with `--host <address>` or `AXEOS_HOST`. With neither, a command uses the saved host, and its result then prints `saved_host` with the address. A command takes one miner, except that the home view, `info`, `asic`, `stats` (without `--samples` and `--columns`) and `firmware` accept `--host` more than once to read several miners, and `restart`, `tuning` and `pool` accept it to change several miners.
- Several miners print `count`, `failed` and a table with one row per miner in the order of the flags: `host`, the fields of the view and `error`. A miner that fails keeps its row with `error` set to `miner_read_failed`, `not_supported` or `invalid_statistics` and `null` values, and the other rows still print. Exit code `1` when any miner failed. The same host twice, or several hosts on `scoreboard`, `logs` or `health`, is a usage error that sends no request.
- `axeos-axi discover` finds miners on the local network and needs no host.
- `host save <address>` saves one default host in a file in the user configuration directory. It sends one `info` request and writes only when an AxeOS miner answers. `host show` prints the saved host and the path, and `host forget` removes the file. No other command writes the file. Run `host save` and `host forget` only when the owner asks for it; `--host` needs no file.
- A result with `saved_host` came from the saved host. Check that it is the miner the task names before a write with `--confirm`.
- Read commands: no command (home view), `info`, `asic`, `stats`, `firmware`, `scoreboard`, `logs`, `health`.
- Each read sends GET requests only. Default views are small; `--fields` selects more.
- `health` is the only command that judges the miner. It reads `info` once and prints a verdict and one row for each rule: `ok`, `failed`, `too_early` or `unknown`, with the value read, the limit and the reason. The limits are fixed: unhealthy below 80 % of the expected 1h hashrate, above 5 % rejected shares, a fault, overheat mode, paused mining, the fallback pool, or a fan at 0 rpm while the miner hashes. Performance rules wait for 10 minutes of uptime, and `rejected_shares` also waits for the first share. A second fan is not judged. A rule with a missing value is `unknown`, and a miner with an `unknown` rule and no failed rule gets the verdict `unknown`, never `healthy`.
- `firmware --check-release` compares the miner firmware version with the newest GitHub release (`up_to_date`, `update_available`, `newer_than_release` or `unknown`). It is the one request that leaves the local network, runs only with this flag, sends no value from the miner and downloads nothing. It takes one miner and works also when the miner has no checksum path. Run it only when the task needs the comparison.
- Run `axeos-axi <command> --help` for the flags of one command.
- `setup install|check|uninstall --agent claude|codex|opencode|all` installs, inspects or removes an opt-in session hook, and `uninstall` removes only the hook that `install` wrote. `check` prints `stale` when the program that the hook names no longer exists, and `install` replaces it. Run it only when the owner asks for it. The hook runs `session dashboard` at the start of each agent session. That command prints the hashrate, temperature, power and firmware version of the miner from the saved host (`host save`), then command hints. It sends one `info` request with a 1 second timeout, and none without a host. It prints no pool user, address, hostname, MAC address or Wi-Fi name. A miner that does not answer gives the line `miner not reachable`. It always exits with `0`.

## Writes need `--confirm`

- `restart`, `tuning` and `pool` change the miner.
- Without `--confirm` each one sends no write request. It prints the present value, the new value and the command that performs the change.
- With `--confirm` each one sends exactly one request for each miner.
- Show the preview to the owner before you run the command with `--confirm`.
- `tuning` accepts only the frequency and core voltage values that the miner lists. The word `default` in place of a value is the default that the miner reports, and the preview prints its number.
- `restart`, `tuning` and `pool` with `--host` more than once change several miners. Name each miner; no flag takes the hosts from `discover`.
- `tuning` takes one value for all named miners; `default` is for each miner its own default. When the list of one miner lacks the value, no miner is changed, and the row of that miner has `value_not_allowed` and, in `allowed`, its list.
- Such a call first reads `info` of each miner, and `tuning` also reads `asic`. Without `--confirm` it prints one row per miner with the present value, the new value and `changes`, and it sends no write request.
- With `--confirm`, when a miner fails that check, no write request goes to any miner. Else the miners get the request one after the other, and the first failed write stops the call.
- Each row has a `result`: `changed`, `failed` or `not_attempted`. Exit code `0` only when each result is `changed`.
- The tool does not set a changed miner back by itself. For `pool` and `tuning`, the column `restore` has the command that sets the previous values of that one miner again. A restart has no reverse.
- Firmware v2.15.3 reports the pool values from before a write on the next read, and the stored values are the new ones; a successful `pool` result and a several-miner result with a changed miner say so in `note`, a `pool_unconfirmed` result says so in `help`, and `pool_failed` and `pool_not_sent` do not say so because no write was stored. A second `pool` call directly after the first reads those old values, so its preview and its restore command can show the value from before the first write, and it sends the old complete record, so it sets the first change back. The restore command that the first call printed is the correct one.

## Private values

- Private values print only on request: the pool user (`--show-user` on `pool`), log lines with the pool user, MAC, Wi-Fi name, payout address and block template unchanged (`--show-private` with `logs --lines` or `logs --follow`), and the API fields `stratumUser`, `fallbackStratumUser`, `pools`, `ssid` and `macAddr` through `--fields`.
- Do not request them unless the task needs them. Do not copy them into shared text.

## Exit codes

- `0` success, `1` request or output error, `2` usage error.
- `health` only: `3` for an unhealthy miner, so a failed read (`1`) and an unhealthy miner stay distinct. The verdict `unknown` exits with `1`.

## Help output

<!-- axeos-axi-help:begin -->
command: home
description: Live mining health
commands: "info, asic, stats, firmware, scoreboard, logs, health, discover, restart, tuning, pool, host, skill, setup, session; axeos-axi <command> --help; discover finds miners without --host; restart, tuning and pool change the miner and send no write request without --confirm; host save stores a default host in one file, and no other command writes that file; skill install writes the agent skill file and needs no host; setup install writes the opt-in session hook of an agent"
flags:
  host: "--host <address>; without it AXEOS_HOST, then the saved host (axeos-axi host --help), and the result then prints saved_host; one of the three is required; HTTP unless a scheme is supplied; repeat --host to read several miners in one call (see several_miners); AXEOS_HOST and the saved host name one miner"
  fields: "--fields <name,...>; default compact view; replaces data fields; accepts view fields and exact API field names; fields the firmware sends only on a condition print null when absent"
  json: "--json; prints the result as one JSON document with the same fields, values and help lines; errors as one JSON object with code, message and help; the exit code is unchanged"
  help: "--help; no network request"
  version: "-v, -V, --version; bare version, or with --json one object {\"version\":\"...\"}; no network request"
several_miners: "with --host given more than once: prints count, failed and miners, a table with one row per miner in the order of the flags; the columns are host, the fields of this view or of --fields, and error; a value the miner does not send is null; a miner that fails has its row with the error code (miner_read_failed, not_supported, invalid_statistics) in error and null in the value columns, and the other miners still print; exit code 1 when any miner failed, 2 for a usage error; the miners are read at the same time, each with the timeout below, and each gets the requests of a single-host call; the same host twice is a usage error and no request is sent; accepted by the home view, info, asic, stats without --samples and --columns, and firmware; scoreboard, logs and health take one miner; restart, tuning and pool accept several miners with the rules of a write (axeos-axi restart --help, axeos-axi tuning --help, axeos-axi pool --help)"
timeout_s: 4
view_fields: "hostname,model,firmware,hashrate,temperature,power_w,efficiency,fan,pool,pool_connection,pool_fallback,shares,best_difficulty,uptime_s,overheat,paused"
conditional_fields: "power_fault,hardware_fault,blockHeight,scriptsig,networkDifficulty,coinbaseValueTotalSatoshis,coinbaseValueUserSatoshis,blockSignals,coinbaseOutputs,hashrateMonitor,mdnsHostname"
private_fields: "info/home/asic: stratumUser,fallbackStratumUser,pools,ssid,macAddr are explicit opt-ins"
examples[4]: axeos-axi --host 192.0.2.10,"axeos-axi --host 192.0.2.10 --fields hashrate,temperature,power_w",axeos-axi --help,axeos-axi --host 192.0.2.10 --host 192.0.2.11
<!-- axeos-axi-help:end -->
