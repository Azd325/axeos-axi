package main

import (
	"context"
	"io"
	"os"
	"runtime/debug"

	"github.com/Azd325/axeos-axi/internal/app"
)

var version = "dev"
var readBuildInfo = debug.ReadBuildInfo

func buildVersion() string {
	if info, ok := readBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

func run(args []string, stdout io.Writer) int {
	a := app.New(os.Getenv)
	a.Version = buildVersion()
	return a.Run(context.Background(), args, stdout)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}
