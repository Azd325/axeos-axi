package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupApp(t *testing.T, home string) *App {
	t.Helper()
	a := New(noHost)
	a.homeDir = func() (string, error) { return home, nil }
	a.executable = func() (string, error) { return "/opt/axeos-axi/axeos-axi", nil }
	return a
}

func claudeSettings(home string) string { return filepath.Join(home, ".claude", "settings.json") }

func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSetupInstallCheckAndUninstallEachAgent(t *testing.T) {
	for _, agent := range []string{"claude", "codex", "opencode"} {
		t.Run(agent, func(t *testing.T) {
			home := t.TempDir()
			a := setupApp(t, home)
			for _, step := range []struct{ action, state string }{
				{"check", "missing"}, {"install", "installed"}, {"check", "installed"},
				{"install", "installed"}, {"uninstall", "removed"}, {"check", "missing"}, {"uninstall", "missing"},
			} {
				code, out := execute(t, a, "setup", step.action, "--agent", agent)
				if code != 0 || !strings.Contains(out, agent+","+step.state+",") {
					t.Fatalf("%s %s: code=%d out=%q", step.action, agent, code, out)
				}
				if strings.Contains(out, home) {
					t.Fatalf("output names the home directory: %q", out)
				}
			}
		})
	}
}

func TestSetupInstallWritesTheSessionCommand(t *testing.T) {
	home := t.TempDir()
	a := setupApp(t, home)
	if code, out := execute(t, a, "setup", "install", "--agent", "all"); code != 0 {
		t.Fatalf("code=%d out=%q", code, out)
	}
	want := "'/opt/axeos-axi/axeos-axi' session dashboard 2>/dev/null || true # axeos-axi-session-hook"
	for _, path := range []string{claudeSettings(home), filepath.Join(home, ".codex", "hooks.json")} {
		entries := readJSONFile(t, path)["hooks"].(map[string]any)["SessionStart"].([]any)
		command := entries[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"]
		if len(entries) != 1 || command != want {
			t.Fatalf("%s: %v", path, entries)
		}
	}
	plugin, err := os.ReadFile(filepath.Join(home, ".config", "opencode", "plugins", "axeos-axi.ts"))
	if err != nil || !strings.Contains(string(plugin), jsonStringCommand(want)) || !strings.Contains(string(plugin), "catch") {
		t.Fatalf("plugin=%q err=%v", plugin, err)
	}
	manifest := readJSONFile(t, filepath.Join(home, ".config", "opencode", "package.json"))
	if manifest["dependencies"].(map[string]any)["@opencode-ai/plugin"] == "" {
		t.Fatalf("manifest=%v", manifest)
	}
}

func TestSetupInstallRepairsTheExecutablePath(t *testing.T) {
	home := t.TempDir()
	a := setupApp(t, home)
	execute(t, a, "setup", "install", "--agent", "claude")
	a.executable = func() (string, error) { return "/new/place/axeos-axi", nil }
	if code, out := execute(t, a, "setup", "install", "--agent", "claude"); code != 0 || !strings.Contains(out, "claude,installed,") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	entries := readJSONFile(t, claudeSettings(home))["hooks"].(map[string]any)["SessionStart"].([]any)
	command := entries[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"].(string)
	if len(entries) != 1 || !strings.HasPrefix(command, "'/new/place/axeos-axi' session dashboard") {
		t.Fatalf("entries=%v", entries)
	}
}

func TestSetupLeavesTheHooksOfOtherToolsUnchanged(t *testing.T) {
	home := t.TempDir()
	original := `{
  "model": "keep",
  "hooks": {
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "other-tool pre"}]}],
    "SessionStart": [
      {"matcher": "", "hooks": [{"type": "command", "command": "router-axi session dashboard"}]},
      {"matcher": "startup", "hooks": [{"type": "command", "command": "other-tool start"}]}
    ]
  }
}`
	path := claudeSettings(home)
	saveFile(t, path, original)
	var want map[string]any
	if err := json.Unmarshal([]byte(original), &want); err != nil {
		t.Fatal(err)
	}
	a := setupApp(t, home)
	if code, out := execute(t, a, "setup", "install", "--agent", "claude"); code != 0 {
		t.Fatalf("code=%d out=%q", code, out)
	}
	installed := readJSONFile(t, path)
	entries := installed["hooks"].(map[string]any)["SessionStart"].([]any)
	if len(entries) != 3 || installed["model"] != "keep" {
		t.Fatalf("installed=%v", installed)
	}
	if code, out := execute(t, a, "setup", "uninstall", "--agent", "claude"); code != 0 || !strings.Contains(out, "claude,removed,") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	got := readJSONFile(t, path)
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("hooks of other tools changed\nwant %s\ngot  %s", wantJSON, gotJSON)
	}
}

func TestSetupUninstallKeepsAHookItDidNotWrite(t *testing.T) {
	home := t.TempDir()
	foreign := `{"hooks":{"SessionStart":[{"matcher":"","hooks":[{"type":"command","command":"'/x/axeos-axi' session dashboard 2>/dev/null || true # axeos-axi-session-hook"}]}]}}`
	saveFile(t, claudeSettings(home), foreign)
	a := setupApp(t, home)
	code, out := execute(t, a, "setup", "uninstall", "--agent", "claude")
	if code != 0 || !strings.Contains(out, "claude,missing,") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if code, out := execute(t, a, "setup", "install", "--agent", "claude"); code != 1 || !strings.Contains(out, "setup_failed") {
		t.Fatalf("install over a hook with no owner record: code=%d out=%q", code, out)
	}
	if data, _ := os.ReadFile(claudeSettings(home)); !strings.Contains(string(data), "/x/axeos-axi") {
		t.Fatalf("foreign hook lost: %s", data)
	}
}

func TestSetupRefusesUnmanagedOpenCodePlugin(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".config", "opencode", "plugins", "axeos-axi.ts")
	saveFile(t, path, "export default {};\n")
	a := setupApp(t, home)
	for _, action := range []string{"install", "check", "uninstall"} {
		if code, _ := execute(t, a, "setup", action, "--agent", "opencode"); code != 1 {
			t.Fatalf("%s: code=%d", action, code)
		}
	}
	if data, _ := os.ReadFile(path); string(data) != "export default {};\n" {
		t.Fatalf("plugin changed: %q", data)
	}
}

func TestSetupUsageErrors(t *testing.T) {
	a := setupApp(t, t.TempDir())
	for _, args := range [][]string{
		{"setup"}, {"setup", "install"}, {"setup", "--agent", "claude"}, {"setup", "install", "--agent", "vim"},
		{"setup", "enable", "--agent", "claude"}, {"setup", "install", "--agent", "claude", "--host", "x"},
		{"setup", "install", "--agent", "claude", "--agent", "codex"}, {"session"}, {"session", "show"}, {"session", "dashboard", "--host", "x"},
		{"info", "--agent", "claude"},
	} {
		if code, out := execute(t, a, args...); code != 2 || !strings.Contains(out, "code: usage") {
			t.Errorf("%v: code=%d out=%q", args, code, out)
		}
	}
	for _, args := range [][]string{{"setup", "--help"}, {"session", "--help"}} {
		if code, out := execute(t, a, args...); code != 0 || !strings.Contains(out, "command: "+args[0]) {
			t.Errorf("%v: code=%d out=%q", args, code, out)
		}
	}
}

func sessionApp(t *testing.T, host string) *App {
	t.Helper()
	a := New(func(name string) string {
		if name == "AXEOS_HOST" {
			return host
		}
		return ""
	})
	return a
}

func TestSessionPrintsSummaryWithoutPrivateValues(t *testing.T) {
	configHome(t)
	info := fixture(t, "info")
	host, requests := miner(t, info)
	a := sessionApp(t, host)
	for _, args := range [][]string{{"session", "dashboard"}, {"session", "dashboard", "--json"}} {
		code, out := execute(t, a, args...)
		if code != 0 {
			t.Fatalf("code=%d out=%q", code, out)
		}
		for _, want := range []string{"hashrate", "temperature", "power", "firmware", "axeos-axi"} {
			if !strings.Contains(out, want) {
				t.Errorf("%v: missing %q in %q", args, want, out)
			}
		}
		private := []string{host, strings.TrimPrefix(host, "http://"), "127.0.0.1"}
		for _, key := range []string{"hostname", "ssid", "macAddr", "stratumUser", "fallbackStratumUser", "stratumURL", "ipv4", "ipv6"} {
			if value, ok := info[key].(string); ok && value != "" {
				private = append(private, value)
			}
		}
		for _, value := range private {
			if strings.Contains(out, value) {
				t.Errorf("%v: output holds private value %q: %q", args, value, out)
			}
		}
	}
	if got := requests(); len(got) != 2 || got[0] != "GET /api/system/info" || got[1] != "GET /api/system/info" {
		t.Fatalf("requests=%v", got)
	}
}

func TestSessionReadsTheSavedHost(t *testing.T) {
	path := configHome(t)
	host, requests := miner(t, fixture(t, "info"))
	saveFile(t, path, host+"\n")
	code, out := execute(t, sessionApp(t, ""), "session", "dashboard")
	if code != 0 || !strings.Contains(out, "hashrate:") || strings.Contains(out, "saved_host") || strings.Contains(out, host) {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if got := requests(); len(got) != 1 {
		t.Fatalf("requests=%v", got)
	}
}

func TestSessionWithoutAHostSendsNoRequest(t *testing.T) {
	configHome(t)
	a := sessionApp(t, "")
	a.Browser = &fakeBrowser{services: advertisedMiners()}
	code, out := execute(t, a, "session", "dashboard")
	if code != 0 || strings.Contains(out, "miner not reachable") || strings.Contains(out, "hashrate") || !strings.Contains(out, "host save <address>") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if calls := a.Browser.(*fakeBrowser).calls; len(calls) != 0 {
		t.Fatalf("browses=%v", calls)
	}
}

func TestSessionInvalidSavedHostIsNotAnError(t *testing.T) {
	path := configHome(t)
	saveFile(t, path, "not a host\n")
	code, out := execute(t, sessionApp(t, ""), "session", "dashboard")
	if code != 0 || strings.Contains(out, "error") || !strings.Contains(out, "host save") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestSessionMinerNotReachable(t *testing.T) {
	configHome(t)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(slow.Close)
	for _, tc := range []struct {
		name string
		host func(t *testing.T) string
	}{
		{"timeout", func(*testing.T) string { return slow.URL }},
		{"server error", func(t *testing.T) string { host, _ := answering(t, 500, "text/plain", "boom"); return host }},
		{"malformed answer", func(t *testing.T) string { host, _ := answering(t, 200, "application/json", "{not json"); return host }},
		{"not found", func(t *testing.T) string { host, _ := answering(t, 404, "text/plain", "no"); return host }},
		{"invalid host", func(*testing.T) string { return "http://user@x/path" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := tc.host(t)
			a := sessionApp(t, host)
			a.sessionTimeout = 150 * time.Millisecond
			start := time.Now()
			var buf bytes.Buffer
			code := a.Run(context.Background(), []string{"session", "dashboard"}, &buf)
			elapsed := time.Since(start)
			out := buf.String()
			if code != 0 || !strings.HasPrefix(out, "miner not reachable\n") || strings.Contains(out, "hashrate") {
				t.Fatalf("code=%d out=%q", code, out)
			}
			for _, value := range []string{host, "127.0.0.1", "boom"} {
				if strings.Contains(out, value) {
					t.Fatalf("output holds %q: %q", value, out)
				}
			}
			if elapsed > 2*time.Second {
				t.Fatalf("took %s", elapsed)
			}
		})
	}
}

func TestSessionTimeoutIsOneSecond(t *testing.T) {
	if got := New(noHost).sessionTimeout; got != time.Second {
		t.Fatalf("timeout=%s", got)
	}
}
