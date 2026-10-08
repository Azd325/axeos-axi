---
name: axeos-axi
description: Read an AxeOS or Bitaxe Bitcoin miner (health verdict, ASIC, statistics, firmware, shares, logs, discovery) and restart it, tune frequency and core voltage, or set its pools with confirmation, through the axeos-axi CLI. Use when an agent works with a miner on the local network.
---

# axeos-axi

`axeos-axi` is a non-interactive CLI for AxeOS miners, including Bitaxe.
It prints compact TOON on stdout; `--json` on any command prints the same result as JSON (`logs --follow --json` prints one JSON object per line). This skill is static text; run the CLI for live miner state.

## Usage

- Name the miner with `--host <address>` or `AXEOS_HOST`. A command takes one miner.
- `axeos-axi discover` finds miners on the local network and needs no host.
- Read commands: no command (home view), `info`, `asic`, `stats`, `firmware`, `scoreboard`, `logs`, `health`.
- Each read sends GET requests only. Default views are small; `--fields` selects more.
- `health` is the only command that judges the miner. It reads `info` once and prints a verdict and one row for each rule: `ok`, `failed`, `too_early` or `unknown`, with the value read, the limit and the reason. The limits are fixed: unhealthy below 80 % of the expected 1h hashrate, above 5 % rejected shares, a fault, overheat mode, paused mining, the fallback pool, or a fan at 0 rpm while the miner hashes. Performance rules wait for 10 minutes of uptime, and `rejected_shares` also waits for the first share. A second fan is not judged. A rule with a missing value is `unknown`, and a miner with an `unknown` rule and no failed rule gets the verdict `unknown`, never `healthy`.
- Run `axeos-axi <command> --help` for the flags of one command.

## Writes need `--confirm`

- `restart`, `tuning` and `pool` change the miner.
- Without `--confirm` each one sends no write request. It prints the present value, the new value and the command that performs the change.
- With `--confirm` each one sends exactly one request.
- Show the preview to the owner before you run the command with `--confirm`.
- `tuning` accepts only the frequency and core voltage values that the miner lists.

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
commands: "info, asic, stats, firmware, scoreboard, logs, health, discover, restart, tuning, pool, skill; axeos-axi <command> --help; discover finds miners without --host; restart, tuning and pool change the miner and send no write request without --confirm; skill install writes the agent skill file and needs no host"
flags:
  host: "--host <address>; default AXEOS_HOST; required for reads; HTTP unless a scheme is supplied"
  fields: "--fields <name,...>; default compact view; replaces data fields; accepts view fields and exact API field names; fields the firmware sends only on a condition print null when absent"
  json: "--json; prints the result as one JSON document with the same fields, values and help lines; errors as one JSON object with code, message and help; the exit code is unchanged"
  help: "--help; no network request"
  version: "-v, -V, --version; bare version, or with --json one object {\"version\":\"...\"}; no network request"
timeout_s: 4
view_fields: "hostname,model,firmware,hashrate,temperature,power_w,efficiency,fan,pool,pool_connection,pool_fallback,shares,best_difficulty,uptime_s,overheat,paused"
conditional_fields: "power_fault,hardware_fault,blockHeight,scriptsig,networkDifficulty,coinbaseValueTotalSatoshis,coinbaseValueUserSatoshis,blockSignals,coinbaseOutputs,hashrateMonitor,mdnsHostname"
private_fields: "info/home/asic: stratumUser,fallbackStratumUser,pools,ssid,macAddr are explicit opt-ins"
examples[3]: axeos-axi --host 192.0.2.10,"axeos-axi --host 192.0.2.10 --fields hashrate,temperature,power_w",axeos-axi --help
<!-- axeos-axi-help:end -->
