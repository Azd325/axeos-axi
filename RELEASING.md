# Releasing

axeos-axi releases are source-only. Do not attach prebuilt binaries.

Set `VERSION` to the SemVer tag being released, merge the release-preparation
pull request, then run this checklist from a clean checkout:

- [ ] Update `main` without creating a merge commit, and confirm it is clean.

  ```sh
  git fetch origin --prune --tags
  git switch main
  git merge --ff-only origin/main
  test -z "$(git status --porcelain)"
  ```

- [ ] Run the complete local check.

  ```sh
  nix develop --no-pure-eval --command check
  ```

- [ ] Audit tracked release contents for miner addresses, hostnames, MAC
      addresses, Wi-Fi names, pool users, payout addresses, and captured live
      responses. Review every match; documentation addresses such as
      `192.0.2.10`, placeholders, and security-policy wording are expected.

  ```sh
  git grep -nPi '(https?://([0-9]{1,3}\.){3}[0-9]{1,3}|([0-9a-f]{2}:){5}[0-9a-f]{2}|ssid|stratum.?user|\.local\b|\b(bc1|[13])[a-zA-Z0-9]{25,39}\b)'
  ```

- [ ] Keep live miner runs local-only. Run them by hand, confirm `CI` is empty,
      and do not retain or publish their output. Record which calls actually
      ran. Send no write: `restart`, `tuning` and `pool` with `--confirm` are
      not part of a release check.

- [ ] Create the signed SemVer tag on the checked `main` commit, verify its
      signature, then push it.

  ```sh
  git tag -s "$VERSION" -m "axeos-axi $VERSION"
  git verify-tag "$VERSION"
  git push origin "$VERSION"
  ```

- [ ] Create a GitHub source release without binary assets, then verify its tag,
      status, URL, and source archives.

  ```sh
  gh release create "$VERSION" --verify-tag --title "axeos-axi $VERSION" --generate-notes
  gh release view "$VERSION" --json tagName,isDraft,isPrerelease,url,assets
  curl -fsSI "https://github.com/Azd325/axeos-axi/archive/refs/tags/$VERSION.tar.gz"
  curl -fsSI "https://github.com/Azd325/axeos-axi/archive/refs/tags/$VERSION.zip"
  ```

- [ ] Verify installation from the published tag in an isolated directory. The
      version fast path must report the released tag rather than `dev`.

  ```sh
  install_dir=$(mktemp -d)
  GOBIN="$install_dir" go install "github.com/Azd325/axeos-axi/cmd/axeos-axi@$VERSION"
  test "$("$install_dir/axeos-axi" --version)" = "$VERSION"
  "$install_dir/axeos-axi" --help
  rm -rf "$install_dir"
  ```

## Recovery

The maintainer decides how a broken release is recovered. A published version
or tag is never replaced, moved or re-created. A fault gets a new version.
GitHub enforces this for releases published in this repository after
2026-10-11: their tags cannot be moved or deleted, and their attached files
cannot be changed or deleted. Earlier releases are not locked.
Choose one of these:

- Point users to the previous working version in the release notes.
- Publish a corrective patch release with a new SemVer tag, using the steps
  above.
- Withdraw or deprecate the version where the Go module ecosystem supports it.

Verify the chosen recovery with the clean-install steps above. Link the
decision from the release notes of the broken version.
