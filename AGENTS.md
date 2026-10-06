# Agent guide

axeos-axi is an independent, agent-ergonomic CLI for reading an AxeOS Bitcoin miner. Its present surface is read-only: GET requests for a home view, `info`, `asic`, `stats`, `firmware`, `scoreboard` and `logs`, and mDNS queries for `discover`.

- Build: `go build -o bin/axeos-axi ./cmd/axeos-axi`
- Test: `go test ./...`
- Lint: `nix develop --no-pure-eval --command check`
- Test data and default output carry no private identifiers.

Read VISION.md before changing command behavior.
