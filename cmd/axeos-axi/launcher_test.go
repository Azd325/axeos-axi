package main

import (
	"bytes"
	"os"
	"runtime"
	"testing"
)

const launcherPath = "../../bin/axeos-axi"

func TestLauncherIsShellScript(t *testing.T) {
	info, err := os.Stat(launcherPath)
	if err != nil {
		t.Fatalf("tracked launcher is missing: %v", err)
	}
	head, err := os.ReadFile(launcherPath)
	if err != nil {
		t.Fatal(err)
	}
	if problem := launcherProblem(info.Mode(), head, runtime.GOOS); problem != "" {
		t.Fatalf("bin/axeos-axi %s; it must stay the tracked launcher, so build into bin/.axeos-axi (run bin/axeos-axi)", problem)
	}
}

func TestLauncherProblem(t *testing.T) {
	script := []byte("#!/usr/bin/env sh\nset -eu\n")
	for name, tc := range map[string]struct {
		mode os.FileMode
		data []byte
		want bool
	}{
		"shell script":   {0o755, script, false},
		"not executable": {0o644, script, true},
		"no shebang":     {0o755, []byte("set -eu\n"), true},
		"other shebang":  {0o755, []byte("#!/usr/bin/env python3\n"), true},
		"ELF binary":     {0o755, []byte("\x7fELF\x02\x01\x01"), true},
		"Mach-O binary":  {0o755, []byte("\xcf\xfa\xed\xfe\x07\x00"), true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := launcherProblem(tc.mode, tc.data, "linux") != ""; got != tc.want {
				t.Errorf("launcherProblem reported problem = %v, want %v", got, tc.want)
			}
		})
	}
}

func launcherProblem(mode os.FileMode, data []byte, goos string) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x7fELF")):
		return "is an ELF binary, not the shell script"
	case bytes.HasPrefix(data, []byte{0xcf, 0xfa, 0xed, 0xfe}), bytes.HasPrefix(data, []byte{0xca, 0xfe, 0xba, 0xbe}):
		return "is a Mach-O binary, not the shell script"
	case !bytes.HasPrefix(data, []byte("#!/usr/bin/env sh\n")):
		return "does not start with the `#!/usr/bin/env sh` shebang"
	case goos != "windows" && mode&0o111 == 0:
		return "is not executable"
	}
	return ""
}
