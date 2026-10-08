package main

import (
	"bytes"
	"runtime/debug"
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

func TestRunVersionOutput(t *testing.T) {
	originalReadBuildInfo, originalVersion := readBuildInfo, version
	t.Cleanup(func() { readBuildInfo, version = originalReadBuildInfo, originalVersion })
	t.Setenv("AXEOS_HOST", "")
	home := t.TempDir()
	for _, name := range []string{"HOME", "XDG_CONFIG_HOME", "AppData"} {
		t.Setenv(name, home)
	}
	tests := []struct {
		name      string
		moduleVer string
		args      []string
		want      string
	}{
		{"released", "v0.1.0", []string{"--version"}, "v0.1.0\n"},
		{"released json", "v0.1.0", []string{"--version", "--json"}, "{\"version\":\"v0.1.0\"}\n"},
		{"local", "(devel)", []string{"--version"}, "dev\n"},
		{"local json", "(devel)", []string{"--version", "--json"}, "{\"version\":\"dev\"}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version = "dev"
			readBuildInfo = func() (*debug.BuildInfo, bool) {
				return &debug.BuildInfo{Main: debug.Module{Version: tt.moduleVer}}, true
			}
			var b bytes.Buffer
			if code := run(tt.args, &b); code != 0 || b.String() != tt.want {
				t.Fatalf("%d %q, want %q", code, b.String(), tt.want)
			}
		})
	}
}
