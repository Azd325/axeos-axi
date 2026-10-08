package main

import (
	"bytes"
	"testing"
)

func TestRunVersionAndUsage(t *testing.T) {
	version = "test-version"
	t.Setenv("AXEOS_HOST", "")
	home := t.TempDir()
	for _, name := range []string{"HOME", "XDG_CONFIG_HOME", "AppData"} {
		t.Setenv(name, home)
	}
	var b bytes.Buffer
	if code := run([]string{"--version"}, &b); code != 0 || b.String() != "test-version\n" {
		t.Fatalf("%d %s", code, b.String())
	}
	b.Reset()
	if code := run(nil, &b); code != 2 {
		t.Fatalf("%d %s", code, b.String())
	}
}
