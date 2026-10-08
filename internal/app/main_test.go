package app

import (
	"fmt"
	"os"
	"testing"
)

var configEnvironment = []string{"HOME", "XDG_CONFIG_HOME", "AppData"}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "axeos-axi-test-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, name := range configEnvironment {
		if err := os.Setenv(name, dir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
