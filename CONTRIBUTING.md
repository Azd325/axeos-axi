# Contributing

Use `nix develop --no-pure-eval` for Go and golangci-lint, then run `check`.
The same checks run in CI: offline tests, `go vet` and `golangci-lint`.

1. Read [VISION.md](VISION.md) before changing command behavior.
2. Make a focused change with tests and documentation for changed contracts.
3. Run `nix develop --no-pure-eval --command check` and commit on a feature branch.
4. Validate and ship through `no-mistakes axi run`; never push the default branch.

Use only sanitized fixtures. Replace hostnames, network addresses, MACs, Wi-Fi names,
stratum users and Bitcoin addresses, including nested pool/coinbase data and scriptsig.
Use documentation addresses such as `192.0.2.10` in examples.
Never send write requests in tests or live verification of this read-only surface.
Keep live checks manual and out of CI. Do not publish their unsanitized output.
