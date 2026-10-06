# Agent guide

axeos-axi is an independent, agent-ergonomic CLI for reading an AxeOS Bitcoin miner, restarting it and tuning it. Its reads are GET requests for a home view, `info`, `asic`, `stats`, `firmware`, `scoreboard` and `logs`, and mDNS queries for `discover`. Its writes are `restart`, one POST request, and `tuning`, one PATCH request for frequency and core voltage; each is sent only with `--confirm`.

- Build: `go build -o bin/axeos-axi ./cmd/axeos-axi`
- Test: `go test ./...`
- Lint: `nix develop --no-pure-eval --command check`
- Test data and default output carry no private identifiers.

Read VISION.md before changing command behavior.
