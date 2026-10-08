# Vision

axeos-axi exists so an agent or technical owner can read and operate AxeOS miners from a predictable command line without the browser UI.
It is an independent tool, not affiliated with the Bitaxe project.
It owns exactly one thing: a small, truthful command-line interface to the miners of one owner on one local network.

## Reads are truthful and small

- Each miner API path is one small command; for each miner it sends exactly one request and changes no existing default view; a command may also read `info` to protect private values; `asic` and `stats` also read `info` for their default views.
- Default views are small; extra fields appear only through explicit `--fields` selection.
- A missing value is `null` or `unknown`, never an inferred healthy state.
- Empty results are definitive; firmware that lacks a path gets one definitive `not_supported` answer, and redirects are not followed.
- Several miners can be read in one call; the result has one row per miner, and a miner that fails is reported in its row and not hidden in a total.
- A health verdict is a separate command with stated rules; it prints which rule failed, and no other command judges the state it reports.
- A command that keeps running (follow, watch) is a separate, explicit mode and requires a time limit; every other command ends by itself.

## The device comes first

- Before a new request is built, its handler is traced in the firmware source.
- A read that can harm the device waits until the vendor releases the fix, then gets a minimum firmware version check; it is not built behind a flag or a warning.
- A firmware family other than AxeOS is supported only for paths that were traced in the source of that family.
- Such a family gets reads only until a real device of that family was tested.

## Writes are previewed and confirmed

- A state-changing command without the confirmation flag sends no state-changing request; it may read the present value to show it, and it prints the present value and the new value.
- With the confirmation flag it sends exactly one request for each miner; the flag is valid in the first call.
- A write can name several miners as an explicit list, and the preview lists each miner with its present value; a write to every discovered miner is refused.
- Restart, tuning (frequency and core voltage) and pool configuration are accepted writes.
- Tuning accepts only values from the list the miner reports; a value outside it is an error.
- The tool makes only changes that one more command of the tool can reverse.
- Firmware update and Wi-Fi configuration are excluded for that reason; reading the newest released version for comparison is not an update.

## Private values and the network

- Values that can identify the owner or the network print only on explicit request, also for free text such as logs.
- Log lines replace the pool user, MAC, Wi-Fi name, Bitcoin addresses and the pool's block template by default; the unchanged lines need an explicit flag.
- The tool talks to miners on the local network only and collects no telemetry.
- The one request that leaves the local network is the read of the newest firmware release; it runs only on an explicit flag and sends no value from the miner.
- Any other request to a host outside the local network needs a new decision in this file.
- The tool stores nothing by itself; a saved default host exists only after an explicit save command, and a result that used the saved host states it.

## The interface follows AXI

- Commands are non-interactive and require no prompts.
- Stdout carries compact TOON results, structured errors and actionable help; stderr is reserved for diagnostics; JSON is available on an explicit flag.
- Exit codes are 0 for success, 1 for errors and 2 for usage errors.
- The health verdict alone uses exit code 3 for an unhealthy miner, so a failed read and an unhealthy miner stay distinct.
- Unknown flags and commands are rejected with valid flags listed.
- The home view starts with `bin:` and `description:` before live state and contextual help, which `--fields` omits; with no host it prints them before the `host_required` error, and the exit code stays 2.
- Every command provides concise `--help`; `-v`, `-V` and `--version` return the version.

## Scope

It is not a monitoring service, not an alerting system and not a firmware updater.
A change aligns when it adds one traced request as one small command, keeps default output small and free of private values, and changes the miner only after a preview and a confirmation.
A change should be resisted when it sends a request whose handler was not traced, changes a default view, stores state without an explicit command, makes a change that one more command cannot reverse, or contacts a host outside the local network other than the named release check.
