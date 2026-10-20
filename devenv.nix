{ pkgs, ... }:
{
  languages.go.enable = true;

  packages = [
    pkgs.golangci-lint
  ];

  scripts.check.exec = ''
    unformatted=$(gofmt -l cmd internal)
    if [ -n "$unformatted" ]; then
      echo "gofmt: unformatted files:" >&2
      echo "$unformatted" >&2
      exit 1
    fi
    go test ./...
    go vet ./...
    golangci-lint run
  '';

  enterShell = ''
    go version
  '';
}
