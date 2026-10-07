---
name: axeos-axi
description: Read an AxeOS or Bitaxe Bitcoin miner (health, ASIC, statistics, firmware, shares, logs, discovery) and restart it, tune frequency and core voltage, or set its pools with confirmation, through the axeos-axi CLI. Use when an agent works with a miner on the local network.
---

# axeos-axi

`axeos-axi` is a non-interactive CLI for AxeOS miners, including Bitaxe.
It prints compact TOON on stdout. This skill is static text; run the CLI for live miner state.

## Usage

- Name the miner with `--host <address>` or `AXEOS_HOST`. A command takes one miner.
- `axeos-axi discover` finds miners on the local network and needs no host.
- Read commands: no command (home view), `info`, `asic`, `stats`, `firmware`, `scoreboard`, `logs`.
- Each read sends GET requests only. Default views are small; `--fields` selects more.
- Run `axeos-axi <command> --help` for the flags of one command.

## Writes need `--confirm`

- `restart`, `tuning` and `pool` change the miner.
- Without `--confirm` each one sends no write request. It prints the present value, the new value and the command that performs the change.
- With `--confirm` each one sends exactly one request.
- Show the preview to the owner before you run the command with `--confirm`.
- `tuning` accepts only the frequency and core voltage values that the miner lists.

## Private values

- Private values print only on request: the pool user (`--show-user` on `pool`), log lines with the pool user, MAC and Wi-Fi name unchanged (`--show-private` with `logs --lines`), and the API fields `stratumUser`, `fallbackStratumUser`, `pools`, `ssid` and `macAddr` through `--fields`.
- Do not request them unless the task needs them. Do not copy them into shared text.

## Exit codes

- `0` success, `1` request or output error, `2` usage error.

## Help output

<!-- axeos-axi-help:begin -->
command: home
description: Live mining health
commands: "info, asic, stats, firmware, scoreboard, logs, discover, restart, tuning, pool, skill; axeos-axi <command> --help; discover finds miners without --host; restart, tuning and pool change the miner and send no write request without --confirm; skill install writes the agent skill file and needs no host"
flags:
  host: "--host <address>; default AXEOS_HOST; required for reads; HTTP unless a scheme is supplied"
  fields: "--fields <name,...>; default compact view; replaces data fields; accepts view fields and exact API field names; fields the firmware sends only on a condition print null when absent"
  help: "--help; no network request"
  version: "-v, -V, --version; bare version; no network request"
timeout_s: 4
view_fields: "hostname,model,firmware,hashrate,temperature,power_w,efficiency,fan,pool,pool_connection,pool_fallback,shares,best_difficulty,uptime_s,overheat,paused"
conditional_fields: "power_fault,hardware_fault,blockHeight,scriptsig,networkDifficulty,coinbaseValueTotalSatoshis,coinbaseValueUserSatoshis,blockSignals,coinbaseOutputs,hashrateMonitor,mdnsHostname"
private_fields: "info/home/asic: stratumUser,fallbackStratumUser,pools,ssid,macAddr are explicit opt-ins"
examples[3]: axeos-axi --host 192.0.2.10,"axeos-axi --host 192.0.2.10 --fields hashrate,temperature,power_w",axeos-axi --help
<!-- axeos-axi-help:end -->
