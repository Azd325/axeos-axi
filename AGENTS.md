# Agent guide

axeos-axi is an independent, agent-ergonomic CLI for reading an AxeOS Bitcoin miner, restarting it, tuning it and setting its pools. Its reads are GET requests for a home view, `info`, `asic`, `stats`, `firmware`, `scoreboard` and `logs`, and mDNS queries for `discover`. Its writes are `restart`, one POST request for each miner, `tuning`, one PATCH request for each miner for frequency and core voltage, and `pool`, one PATCH request for each miner for the URL, port and user of the primary and the fallback pool; each is sent only with `--confirm`.

- Build and run: `bin/axeos-axi`, which builds `./cmd/axeos-axi` into `bin/.axeos-axi`; `go build -o bin/axeos-axi` would overwrite the tracked launcher
- Test: `go test ./...`
- Lint: `nix develop --no-pure-eval --command check`
- Test data and default output carry no private identifiers.

Read VISION.md before changing command behavior.
