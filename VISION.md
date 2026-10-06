# Vision

axeos-axi exists so an agent or technical owner can read an AxeOS miner from a predictable command line without the browser UI.
It is an independent tool, not affiliated with the Bitaxe project.

## Decided

- The present command surface is read-only.
- Unknown flags and commands are rejected with valid flags listed.
- The home view starts with `bin:` and `description:` before live state and contextual help.

## Proposed, not yet approved

- Commands are non-interactive and require no prompts.
- Stdout carries compact TOON results, structured errors and actionable help; stderr is reserved for diagnostics.
- Exit codes are 0 for success, 1 for errors and 2 for usage errors.
- Small default schemas expose extra fields only through explicit `--fields` selection.
- Empty results are definitive; useful aggregate state avoids follow-up reads.
- Every command provides concise `--help`; `-v`, `-V` and `--version` return the version.
- Private identifiers (pool user, Wi-Fi name and MAC) do not appear in default output, except in the log lines that `logs` prints as the miner wrote them.
- A later state-changing command shows its effect first and requires an explicit confirmation flag.
- Firmware update is excluded.
- The tool operates on the local network only and collects no telemetry.
