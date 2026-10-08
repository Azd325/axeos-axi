package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Azd325/axeos-axi/internal/hostfile"
)

const hostSaveRead = "GET /api/system/info"

func configHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, name := range configEnvironment {
		t.Setenv(name, home)
	}
	path, err := hostfile.Path()
	if err != nil || !strings.HasPrefix(path, home+string(filepath.Separator)) {
		t.Fatalf("host file %q is outside the temporary home %q: %v", path, home, err)
	}
	return path
}

func saveFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func answering(t *testing.T, status int, contentType, body string) (string, func() []string) {
	t.Helper()
	return restartMiner(t, restartAnswer(status, contentType, body))
}

func TestHostSaveChecksTheMinerAndWritesOneFile(t *testing.T) {
	path := configHome(t)
	host, calls := miner(t, fixture(t, "info"))
	code, out := execute(t, New(noHost), "host", "save", host)
	want := "saved_host: \"" + host + "\"\npath: " + tildePath(path) + "\nhelp[2]: "
	if code != 0 || !strings.HasPrefix(out, want) {
		t.Fatalf("code=%d\n%s\nwant prefix\n%s", code, out, want)
	}
	if strings.Join(calls(), ",") != hostSaveRead {
		t.Fatalf("requests=%v", calls())
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != host+"\n" {
		t.Fatalf("file=%q error=%v", content, err)
	}
	if runtime.GOOS != "windows" {
		file, _ := os.Stat(path)
		dir, _ := os.Stat(filepath.Dir(path))
		if file.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700 {
			t.Fatalf("file mode=%v directory mode=%v", file.Mode().Perm(), dir.Mode().Perm())
		}
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Fatalf("temporary file left behind: %v", entries)
	}
}

func TestHostSaveStoresTheValidatedAddress(t *testing.T) {
	path := configHome(t)
	host, _ := miner(t, fixture(t, "info"))
	typed := strings.ToUpper(strings.TrimPrefix(host, "http://")) + "/#/"
	code, out := execute(t, New(noHost), "host", "save", typed)
	content, _ := os.ReadFile(path)
	if code != 0 || string(content) != host+"\n" || !strings.Contains(out, "saved_host: \""+host+"\"\n") {
		t.Fatalf("code=%d file=%q\n%s", code, content, out)
	}
}

func TestHostSaveReplacesTheSavedHost(t *testing.T) {
	path := configHome(t)
	host, _ := miner(t, fixture(t, "info"))
	other, _ := miner(t, fixture(t, "info"))
	for _, address := range []string{host, host, other} {
		if code, out := execute(t, New(noHost), "host", "save", address); code != 0 {
			t.Fatal(out)
		}
	}
	if content, _ := os.ReadFile(path); string(content) != other+"\n" {
		t.Fatalf("file=%q", content)
	}
}

func TestHostSaveFailedCheckWritesNothing(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	unreachable := closed.URL
	closed.Close()
	for _, tc := range []struct {
		name, contentType, body string
		status                  int
		exit                    int
		code                    string
	}{
		{name: "unreachable", exit: 1, code: "miner_read_failed"},
		{name: "not found", status: http.StatusNotFound, exit: 1, code: "miner_read_failed"},
		{name: "web page", status: http.StatusOK, contentType: "text/html", body: "<html></html>", exit: 1, code: "miner_read_failed"},
		{name: "empty object", status: http.StatusOK, contentType: "application/json", body: `{}`, exit: 1, code: "not_a_miner"},
		{name: "other API", status: http.StatusOK, contentType: "application/json", body: `{"version":"1.0"}`, exit: 1, code: "not_a_miner"},
		{name: "no version", status: http.StatusOK, contentType: "application/json", body: `{"ASICModel":"BM1370","version":""}`, exit: 1, code: "not_a_miner"},
		{name: "version is a number", status: http.StatusOK, contentType: "application/json", body: `{"ASICModel":"BM1370","version":2}`, exit: 1, code: "not_a_miner"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := configHome(t)
			host, calls := unreachable, func() []string { return []string{hostSaveRead} }
			if tc.status != 0 {
				host, calls = answering(t, tc.status, tc.contentType, tc.body)
			}
			code, out := execute(t, New(noHost), "host", "save", host)
			if code != tc.exit || !strings.Contains(out, "code: "+tc.code+"\n") || !strings.Contains(out, "nothing was saved") || !strings.Contains(out, "axeos-axi host save <address>") {
				t.Fatalf("code=%d\n%s", code, out)
			}
			if strings.Join(calls(), ",") != hostSaveRead {
				t.Fatalf("requests=%v", calls())
			}
			if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
				t.Fatalf("failed check wrote into the configuration directory: %v", err)
			}

			saveFile(t, path, "http://192.0.2.10\n")
			if code, out := execute(t, New(noHost), "host", "save", host); code != tc.exit {
				t.Fatalf("code=%d\n%s", code, out)
			}
			if content, _ := os.ReadFile(path); string(content) != "http://192.0.2.10\n" {
				t.Fatalf("failed check changed the saved host: %q", content)
			}
		})
	}
}

func TestHostUsageErrorsSendAndWriteNothing(t *testing.T) {
	path := configHome(t)
	host, calls := miner(t, fixture(t, "info"))
	for _, args := range [][]string{
		{"host", "save"},
		{"host", "remove"},
		{"host", "show", "extra"},
		{"host", "forget", "extra"},
		{"host", "save", host, host},
		{"host", "save", "--host", host},
		{"host", "save", host, "--host", host},
		{"host", "show", "--fields", "saved_host"},
		{"host", "forget", "--confirm"},
		{"host", "save", host, "--path", filepath.Dir(path)},
		{"host", "save", host, "--bogus"},
		{"host", "save", host, "--json=1"},
	} {
		code, out := execute(t, New(func(string) string { return host }), args...)
		if code != 2 || !strings.Contains(out, "code") || !strings.Contains(out, "usage") {
			t.Errorf("%v: code=%d output=%s", args, code, out)
		}
	}
	code, out := execute(t, New(noHost), "host", "save", "http://")
	if code != 2 || !strings.Contains(out, "code: invalid_host") || !strings.Contains(out, "help: axeos-axi host save 192.0.2.10") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if len(calls()) != 0 {
		t.Fatalf("usage error sent requests: %v", calls())
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("usage error wrote into the configuration directory: %v", err)
	}
}

func TestHostAloneIsAUsageError(t *testing.T) {
	path := configHome(t)
	code, out := execute(t, New(noHost), "host")
	if code != 2 || !strings.Contains(out, "code: usage") || !strings.Contains(out, hostUsage) {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("usage error wrote into the configuration directory: %v", err)
	}
}

func TestHostHelp(t *testing.T) {
	path := configHome(t)
	_, help := execute(t, New(noHost), "host", "--help")
	for _, args := range [][]string{{"host", "save", "--help"}, {"host", "show", "--help"}, {"--help", "host", "forget"}} {
		code, out := execute(t, New(noHost), args...)
		if code != 0 || out != help || !strings.HasPrefix(out, "command: host\n") {
			t.Errorf("%v: code=%d\n%s", args, code, out)
		}
	}
	for _, want := range []string{"host save <address>", "host show", "host forget", "saved_host", "GET /api/system/info", "saved_host_invalid"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q", want)
		}
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("help wrote into the configuration directory: %v", err)
	}
}

func TestHostShowAndForget(t *testing.T) {
	path := configHome(t)
	host, calls := miner(t, fixture(t, "info"))
	shown := tildePath(path)

	code, out := execute(t, New(noHost), "host", "show")
	if code != 0 || out != "saved_host: null\npath: "+shown+"\nstate: no host is saved\nhelp[1]: axeos-axi discover finds miners on the local network; then axeos-axi host save <address> to save a default host\n" {
		t.Fatalf("code=%d\n%s", code, out)
	}
	code, out = execute(t, New(noHost), "host", "forget")
	if code != 0 || out != "removed: false\npath: "+shown+"\nstate: no host was saved; nothing was removed\n" {
		t.Fatalf("code=%d\n%s", code, out)
	}

	saveFile(t, path, host+"\n")
	code, out = execute(t, New(noHost), "host", "show")
	if code != 0 || out != "saved_host: \""+host+"\"\npath: "+shown+"\n" {
		t.Fatalf("code=%d\n%s", code, out)
	}
	code, out = execute(t, New(noHost), "host", "show", "--json")
	if code != 0 || !strings.HasPrefix(out, `{"saved_host":"`+host+`","path":`) {
		t.Fatalf("code=%d\n%s", code, out)
	}
	code, out = execute(t, New(noHost), "host", "forget")
	if code != 0 || out != "removed: true\npath: "+shown+"\n" {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("forget kept the file: %v", err)
	}
	code, out = execute(t, New(noHost), "host", "forget")
	if code != 0 || !strings.HasPrefix(out, "removed: false\n") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	code, out = execute(t, New(noHost), "info")
	if code != 2 || !strings.Contains(out, "code: host_required") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if len(calls()) != 0 {
		t.Fatalf("show and forget sent requests: %v", calls())
	}
}

func TestHostResolutionOrder(t *testing.T) {
	path := configHome(t)
	saved, savedCalls := miner(t, renamed(t, "saved-miner"))
	fromEnv, envCalls := miner(t, renamed(t, "environment-miner"))
	fromFlag, flagCalls := miner(t, renamed(t, "flag-miner"))
	saveFile(t, path, saved+"\n")

	code, out := execute(t, New(noHost), "info")
	if code != 0 || !strings.HasPrefix(out, "saved_host: \""+saved+"\"\nhostname: saved-miner\n") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	code, out = execute(t, New(func(string) string { return fromEnv }), "info")
	if code != 0 || !strings.HasPrefix(out, "hostname: environment-miner\n") || strings.Contains(out, "saved_host") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	code, out = execute(t, New(func(string) string { return fromEnv }), "info", "--host", fromFlag)
	if code != 0 || !strings.HasPrefix(out, "hostname: flag-miner\n") || strings.Contains(out, "saved_host") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	code, out = execute(t, New(noHost), "info", "--host", fromFlag)
	if code != 0 || !strings.HasPrefix(out, "hostname: flag-miner\n") || strings.Contains(out, "saved_host") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if len(savedCalls()) != 1 || len(envCalls()) != 1 || len(flagCalls()) != 2 {
		t.Fatalf("saved=%v environment=%v flag=%v", savedCalls(), envCalls(), flagCalls())
	}
}

func TestFlagAndEnvironmentOutputIsUnchangedByASavedHost(t *testing.T) {
	path := configHome(t)
	host, _ := miner(t, fixture(t, "info"))
	other, otherCalls := miner(t, renamed(t, "saved-miner"))
	for _, args := range [][]string{nil, {"info"}, {"asic"}, {"stats"}, {"firmware"}, {"scoreboard"}, {"health"}, {"restart"}, {"info", "--fields", "nonsense"}, {"--json"}} {
		for _, source := range []string{"flag", "environment"} {
			env, withHost := host, args
			if source == "flag" {
				env, withHost = "", append([]string{"--host", host}, args...)
			}
			a := New(func(string) string { return env })
			_ = os.Remove(path)
			wantCode, want := execute(t, a, withHost...)
			saveFile(t, path, other+"\n")
			code, out := execute(t, a, withHost...)
			if code != wantCode || out != want {
				t.Errorf("%s %v: a saved host changed the result\n%s\nwant\n%s", source, args, out, want)
			}
			saveFile(t, path, "malformed")
			code, out = execute(t, a, withHost...)
			if code != wantCode || out != want {
				t.Errorf("%s %v: a malformed file changed the result\n%s\nwant\n%s", source, args, out, want)
			}
		}
	}
	if len(otherCalls()) != 0 {
		t.Fatalf("the saved host got requests: %v", otherCalls())
	}
}

func TestSavedHostIsStatedInOneLine(t *testing.T) {
	path := configHome(t)
	host, _ := miner(t, fixture(t, "info"))
	saveFile(t, path, host+"\n")
	line := "saved_host: \"" + host + "\"\n"
	for _, args := range [][]string{{"info"}, {"asic"}, {"stats"}, {"firmware"}, {"scoreboard"}, {"health"}, {"restart"}, {"info", "--fields", "version"}, {"firmware", "--fields", "nonsense"}} {
		_, want := execute(t, New(noHost), append([]string{"--host", host}, args...)...)
		_, out := execute(t, New(noHost), args...)
		if out != line+want {
			t.Errorf("%v:\n%s\nwant\n%s", args, out, line+want)
		}
	}
	_, want := execute(t, New(noHost), "--host", host)
	_, out := execute(t, New(noHost))
	rows := strings.SplitAfterN(want, "\n", 3)
	if !strings.HasPrefix(rows[0], "bin: ") || !strings.HasPrefix(rows[1], "description: ") || out != rows[0]+rows[1]+line+rows[2] {
		t.Fatalf("home view:\n%s", out)
	}
	_, out = execute(t, New(noHost), "info", "--json")
	if !strings.HasPrefix(out, `{"saved_host":"`+host+`","hostname":`) || strings.Count(out, "\n") != 1 {
		t.Fatalf("json:\n%s", out)
	}
}

func TestSavedHostIsStatedOnceWhileLogsFollow(t *testing.T) {
	path := configHome(t)
	_, out := runParityIn(t, parityCase{args: []string{"logs", "--follow", "2"}, saved: true}, path)
	if strings.Count(out, "saved_host: ") != 1 || !strings.HasPrefix(out, "saved_host: \"HOST\"\nfollow_limit_s: 2\n") || !strings.Contains(out, "line_2: ") {
		t.Fatalf("%s", out)
	}
}

func TestSavedHostIsStatedOnAFailedRead(t *testing.T) {
	path := configHome(t)
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	host := closed.URL
	closed.Close()
	saveFile(t, path, host+"\n")
	code, out := execute(t, New(noHost), "info")
	if code != 1 || !strings.HasPrefix(out, "saved_host: \""+host+"\"\nerror:\n  code: miner_read_failed\n") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	code, out = execute(t, New(noHost))
	if code != 1 || !strings.HasPrefix(out, "saved_host: \""+host+"\"\nerror:\n  code: miner_read_failed\n") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if !strings.Contains(out, "help: "+savedConnectivityHelp+"\n") || strings.Contains(out, "AXEOS_HOST") {
		t.Fatalf("help names a source that was not used:\n%s", out)
	}
	code, out = execute(t, New(noHost), "info", "--host", host)
	if code != 1 || !strings.Contains(out, "help: "+connectivityHelp+"\n") || strings.Contains(out, "saved_host") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestInvalidSavedHostFileIsAnError(t *testing.T) {
	host, calls := miner(t, fixture(t, "info"))
	for name, content := range map[string]string{
		"empty":             "",
		"blank line":        "\n",
		"no scheme":         "192.0.2.10\n",
		"two hosts":         "http://192.0.2.10\nhttp://192.0.2.11\n",
		"two newlines":      "http://192.0.2.10\n\n",
		"leading space":     " http://192.0.2.10\n",
		"path":              "http://192.0.2.10/api\n",
		"default port":      "http://192.0.2.10:80\n",
		"upper case":        "http://MINER.example\n",
		"credentials":       "http://user:secret@192.0.2.10\n",
		"other content":     "{\"host\":\"192.0.2.10\"}\n",
		"long":              "http://" + strings.Repeat("a", 4096) + "\n",
		"carriage return":   "http://192.0.2.10\r\n",
		"fragment":          "http://192.0.2.10/#/\n",
		"comment after it":  "http://192.0.2.10 # miner\n",
		"byte order mark":   "\ufeffhttp://192.0.2.10\n",
		"not valid as text": "\xff\xfe",
	} {
		t.Run(name, func(t *testing.T) {
			path := configHome(t)
			saveFile(t, path, content)
			shown := tildePath(path)
			for _, args := range [][]string{{"info"}, {"host", "show"}, {"restart", "--confirm"}} {
				code, out := execute(t, New(noHost), args...)
				if code != 1 || !strings.HasPrefix(out, "error:\n  code: saved_host_invalid\n") || !strings.Contains(out, "the saved host file "+shown+" does not hold one address") || !strings.Contains(out, "axeos-axi host forget removes the file") {
					t.Fatalf("%v: code=%d\n%s", args, code, out)
				}
			}
			code, out := execute(t, New(noHost))
			if code != 1 || !strings.HasPrefix(out, "bin: ") || !strings.Contains(out, "\nerror:\n  code: saved_host_invalid\n") {
				t.Fatalf("home view: code=%d\n%s", code, out)
			}
			code, out = execute(t, New(noHost), "info", "--host", host, "--fields", "version")
			if code != 0 || out != "version: v2.15.3\n" {
				t.Fatalf("--host: code=%d\n%s", code, out)
			}
			code, out = execute(t, New(noHost), "host", "forget")
			if code != 0 || !strings.HasPrefix(out, "removed: true\n") {
				t.Fatalf("forget: code=%d\n%s", code, out)
			}
			code, out = execute(t, New(noHost), "info")
			if code != 2 || !strings.Contains(out, "code: host_required") {
				t.Fatalf("after forget: code=%d\n%s", code, out)
			}
		})
	}
	if len(calls()) != 17 {
		t.Fatalf("an invalid file sent requests besides the --host reads: %v", calls())
	}
}

func TestUnreadableSavedHostFileIsAnError(t *testing.T) {
	path := configHome(t)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"info"}, {"host", "show"}} {
		code, out := execute(t, New(noHost), args...)
		if code != 1 || !strings.Contains(out, "code: saved_host_invalid") || !strings.Contains(out, "the saved host file "+tildePath(path)+" could not be read") || !strings.Contains(out, "axeos-axi host forget") {
			t.Fatalf("%v: code=%d\n%s", args, code, out)
		}
	}
}

func TestHostSaveReplacesAnInvalidFile(t *testing.T) {
	path := configHome(t)
	host, _ := miner(t, fixture(t, "info"))
	saveFile(t, path, "malformed")
	code, out := execute(t, New(noHost), "host", "save", host)
	content, _ := os.ReadFile(path)
	if code != 0 || string(content) != host+"\n" {
		t.Fatalf("code=%d file=%q\n%s", code, content, out)
	}
}

func TestMissingHostNamesHostSave(t *testing.T) {
	configHome(t)
	for _, args := range [][]string{nil, {"info"}, {"restart"}} {
		code, out := execute(t, New(noHost), args...)
		if code != 2 || !strings.Contains(out, "code: host_required") || !strings.Contains(out, "message: set --host <address> or AXEOS_HOST\n") || !strings.Contains(out, "axeos-axi host save <address>") {
			t.Errorf("%v: code=%d\n%s", args, code, out)
		}
	}
}

func TestWritesWithTheSavedHostStateTheHost(t *testing.T) {
	path := configHome(t)
	host, calls := restartMiner(t, accepted())
	saveFile(t, path, host+"\n")
	stated := "saved_host: \"" + host + "\"\nhost: \"" + host + "\"\n"

	code, out := execute(t, New(noHost), "restart")
	if code != 0 || !strings.HasPrefix(out, stated+"request: POST /api/system/restart\nsent: false\n") || !strings.Contains(out, "execute: \"axeos-axi restart --host '"+host+"' --confirm\"\n") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if len(calls()) != 0 {
		t.Fatalf("preview sent requests: %v", calls())
	}
	code, out = execute(t, New(noHost), "restart", "--confirm")
	if code != 0 || !strings.HasPrefix(out, stated+"request: POST /api/system/restart\nsent: true\n") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if strings.Join(calls(), ",") != "POST /api/system/restart" {
		t.Fatalf("requests=%v", calls())
	}

	tuned, tuningCalls := tuningMiner(t, nil, settingsSaved)
	saveFile(t, path, tuned+"\n")
	stated = "saved_host: \"" + tuned + "\"\nhost: \"" + tuned + "\"\n"
	code, out = execute(t, New(noHost), "tuning", "--frequency", "550")
	if code != 0 || !strings.HasPrefix(out, stated) || !strings.Contains(out, "sent: false") || !strings.Contains(out, "axeos-axi tuning --host '"+tuned+"' --frequency 550 --confirm") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	code, out = execute(t, New(noHost), "tuning", "--frequency", "550", "--confirm")
	if code != 0 || !strings.HasPrefix(out, stated) || !strings.Contains(out, "sent: true") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if tuningCalls() != tuningReads+","+tuningReads+`,PATCH /api/system {"frequency":550}` {
		t.Fatalf("requests=%s", tuningCalls())
	}

	pooled, poolCalls := poolMiner(t, nil, settingsSaved)
	saveFile(t, path, pooled+"\n")
	stated = "saved_host: \"" + pooled + "\"\nhost: \"" + pooled + "\"\n"
	code, out = execute(t, New(noHost), "pool", "--port", "3334")
	if code != 0 || !strings.HasPrefix(out, stated) || !strings.Contains(out, "sent: false") || !strings.Contains(out, "axeos-axi pool --host '"+pooled+"' --port=3334 --confirm") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	code, out = execute(t, New(noHost), "pool", "--port", "3334", "--confirm")
	if code != 0 || !strings.HasPrefix(out, stated) || !strings.Contains(out, "sent: true") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if strings.Count(poolCalls(), "PATCH /api/system") != 1 {
		t.Fatalf("requests=%s", poolCalls())
	}
}

func TestOnlyHostSaveWritesTheFile(t *testing.T) {
	path := configHome(t)
	host, _ := miner(t, fixture(t, "info"))
	restarted, _ := restartMiner(t, accepted())
	a := New(func(string) string { return host })
	a.Browser = &fakeBrowser{services: advertisedMiners()}
	for _, args := range [][]string{nil, {"info"}, {"asic"}, {"stats"}, {"firmware"}, {"scoreboard"}, {"health"}, {"discover"}, {"restart"}, {"restart", "--host", restarted, "--confirm"}, {"--help"}, {"--version"}, {"host"}, {"host", "show"}, {"host", "forget"}, {"--host", host, "--host", restarted}} {
		execute(t, a, args...)
		if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
			t.Fatalf("%v wrote into the configuration directory: %v", args, err)
		}
	}
}

func TestHostFlagHelpStatesTheOrder(t *testing.T) {
	for _, command := range []string{"", "info", "asic", "stats", "firmware", "scoreboard", "logs", "health", "restart", "tuning", "pool"} {
		args := []string{"--help"}
		if command != "" {
			args = append([]string{command}, args...)
		}
		_, out := execute(t, New(noHost), args...)
		if !strings.Contains(out, "host: \"--host <address>; without it AXEOS_HOST, then the saved host (axeos-axi host --help), and the result then prints saved_host; one of the three is required; ") {
			t.Errorf("%q help:\n%s", command, out)
		}
	}
}
