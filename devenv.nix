{ pkgs, ... }:
{
  languages.go.enable = true;

  packages = [
    pkgs.golangci-lint
  ];

  scripts.check.exec = ''
    go test ./...
    go vet ./...
    golangci-lint run
  '';

  enterShell = ''
    go version
  '';
}
