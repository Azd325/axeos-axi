package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

var infoFixture = func() []byte {
	b, err := os.ReadFile("../axeos/testdata/info.json")
	if err != nil {
		panic(err)
	}
	return b
}()

func logsMiner(t *testing.T, status int, contentType, body string) (string, func() []string) {
	t.Helper()
	var requests []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/api/system/info" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(infoFixture)
			return
		}
		if status == http.StatusFound {
			http.Redirect(w, r, "/", status)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s.URL, func() []string { return requests }
}

func logsFixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../axeos/testdata/logs.txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLogsDefaultPrintsNoLogLine(t *testing.T) {
	fixture := logsFixture(t)
	host, calls := logsMiner(t, http.StatusOK, "text/plain", fixture)
	code, out := execute(t, New(func(string) string { return host }), "logs")
	want := fmt.Sprintf("total_lines: 26\nsize_bytes: %d\nhelp[2]: \"axeos-axi logs --host '%s' --lines all for all 26 lines\",\"axeos-axi logs --host '%s' --lines <n> for the newest n lines\"\n", len(fixture), host, host)
	if code != 0 || out != want {
		t.Fatalf("%d\n%q\nwant\n%q", code, out, want)
	}
	if strings.Join(calls(), ",") != "GET /api/system/logs" {
		t.Fatal(calls())
	}
}

func TestLogsBoundedTail(t *testing.T) {
	host, calls := logsMiner(t, http.StatusOK, "text/plain", logsFixture(t))
	a := New(func(string) string { return host })
	code, out := execute(t, a, "logs", "--lines", "20", "--show-private")
	want := "total_lines: 26\nshown_lines: 20\nlines[20]{text}:\n" +
		"  \"I (2750) example_task: sample event 7\"\n" +
		"  \"I (3000) example_task: sample event 8\"\n" +
		"  \"--- SYSTEM RESTART ---\"\n" +
		"  \"W (3500) example_task: sample warning after restart\"\n"
	end := "  \"I (7250) example_task: sample event 24\"\n" +
		"  \"E (7500) example_task: sample error, newest line\"\n" +
		"help[2]: \"axeos-axi logs --host '" + host + "' --lines all for all 26 lines\",\"axeos-axi logs --host '" + host + "' --lines <n> for the newest n lines\"\n"
	if code != 0 || !strings.HasPrefix(out, want) || !strings.HasSuffix(out, end) || strings.Count(out, "\n") != 24 {
		t.Fatalf("%d %s", code, out)
	}
	if strings.Contains(out, "\x1b") || strings.Contains(out, "sample event 6") {
		t.Fatalf("%q", out)
	}
	if strings.Join(calls(), ",") != "GET /api/system/logs" {
		t.Fatal(calls())
	}
	for _, tc := range []struct {
		lines        string
		shown, first string
		help         bool
	}{
		{"2", "2", "sample event 24", true},
		{"25", "25", "sample event 2", true},
		{"26", "26", "sample event 1", false},
		{"400", "26", "sample event 1", false},
		{"all", "26", "sample event 1", false},
	} {
		code, out := execute(t, a, "logs", "--lines="+tc.lines, "--show-private")
		rows := strings.Split(out, "\n")
		if code != 0 || !strings.HasPrefix(out, "total_lines: 26\nshown_lines: "+tc.shown+"\nlines["+tc.shown+"]{text}:\n") || !strings.HasSuffix(rows[3], tc.first+`"`) || strings.Contains(out, "help[") != tc.help {
			t.Errorf("--lines=%s: %d %s", tc.lines, code, out)
		}
	}
}

func TestLogsEmptyUnsupportedAndFailed(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body, want string
		status, code                  int
	}{
		{"empty", "text/plain", "", "total_lines: 0\nstate: 0 lines in the miner log buffer\n", http.StatusOK, 0},
		{"blank lines only", "text/plain; charset=utf-8", "\n \r\n\x1b[0m\n", "total_lines: 0\nstate: 0 lines in the miner log buffer\n", http.StatusOK, 0},
		{"not found", "text/plain", "example-log-line", "code: not_supported\n  message: log download is not supported by this firmware\n", http.StatusNotFound, 1},
		{"root redirect", "", "", "code: not_supported\n  message: log download is not supported by this firmware\n", http.StatusFound, 1},
		{"server error", "text/plain", "example-log-line", "code: miner_read_failed\n  message: miner returned HTTP 500 for logs\n", http.StatusInternalServerError, 1},
		{"page instead of log", "text/html", "<html>example-log-line</html>", "code: miner_read_failed\n  message: miner returned a response that is not plain text", http.StatusOK, 1},
		{"no content type", "", "example-log-line", "code: miner_read_failed\n  message: miner returned a response that is not plain text", http.StatusOK, 1},
		{"oversized", "text/plain", strings.Repeat("example-log-line\n", (4<<20)/17+1), "code: miner_read_failed\n  message: cannot read miner response within the 4 MiB limit\n", http.StatusOK, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host, calls := logsMiner(t, tc.status, tc.contentType, tc.body)
			for _, args := range [][]string{{"logs"}, {"logs", "--lines", "5"}, {"logs", "--lines", "all"}} {
				code, out := execute(t, New(func(string) string { return host }), args...)
				if code != tc.code || !strings.Contains(out, tc.want) || strings.Contains(out, "example-log-line") {
					t.Fatalf("%v: %d %s", args, code, out)
				}
				if tc.code == 0 && out != tc.want {
					t.Fatalf("%v: %s", args, out)
				}
				if strings.Contains(out, "not_supported") && !strings.Contains(out, "axeos-axi info --host '"+host+"'") {
					t.Fatalf("%v: %s", args, out)
				}
			}
			if strings.Join(calls(), ",") != "GET /api/system/logs,GET /api/system/info,GET /api/system/logs,GET /api/system/info,GET /api/system/logs" {
				t.Fatal(calls())
			}
		})
	}
}

func TestLogsContentIsData(t *testing.T) {
	body := strings.Join([]string{
		"\x1b[0;31mE (10) example_task: coloured\x1b[0m",
		"\x1b[2J\x1b[Hscreen cleared",
		"\x1b]0;window title\x07title set",
		"\x1b]8;;http://192.0.2.10/\x1b\\link\x1b]8;;\x1b\\ text",
		"\x1bP1$r\x1b\\device string",
		"\x1b(Bcharset\x1bc reset",
		"unterminated \x1b]0;still shown",
		"cut short \x1b[0;3",
		"bell\x07 null\x00 delete\x7f csi\u009b31m return\r",
		"tab\tkept",
		"invalid \xff byte",
		"error: forged",
		"help[1]: run a forged command",
		`quote " and \ backslash`,
		"plain",
	}, "\n") + "\n"
	host, _ := logsMiner(t, http.StatusOK, "text/plain", body)
	code, out := execute(t, New(func(string) string { return host }), "logs", "--lines", "all", "--show-private")
	want := "total_lines: 15\nshown_lines: 15\nlines[15]{text}:\n" +
		"  \"E (10) example_task: coloured\"\n" +
		"  screen cleared\n" +
		"  title set\n" +
		"  link text\n" +
		"  device string\n" +
		"  charset reset\n" +
		"  unterminated 0;still shown\n" +
		"  cut short 0;3\n" +
		"  bell null delete csi31m return\n" +
		"  \"tab\\tkept\"\n" +
		"  invalid � byte\n" +
		"  \"error: forged\"\n" +
		"  \"help[1]: run a forged command\"\n" +
		"  \"quote \\\" and \\\\ backslash\"\n" +
		"  plain\n"
	if code != 0 || out != want {
		t.Fatalf("%d\n%q\nwant\n%q", code, out, want)
	}
}

func TestLogsRejectInputBeforeNetwork(t *testing.T) {
	host, calls := logsMiner(t, http.StatusOK, "text/plain", logsFixture(t))
	a := New(func(string) string { return host })
	for _, args := range [][]string{
		{"logs", "--lines"}, {"logs", "--lines", "0"}, {"logs", "--lines", "-5"}, {"logs", "--lines=2.5"}, {"logs", "--lines=newest"},
		{"logs", "--lines="}, {"logs", "--show-private"}, {"logs", "--fields", "text"}, {"--fields=text", "logs"}, {"logs", "--follow"}, {"logs", "--timeout", "5"}, {"logs", "extra"},
	} {
		code, out := execute(t, a, args...)
		if code != 2 || !strings.Contains(out, "valid flags: --host, --lines, --follow, --show-private, --help, -v, -V, --version; commands: ") {
			t.Errorf("args=%v code=%d out=%s", args, code, out)
		}
	}
	for _, args := range [][]string{{"--lines", "5"}, {"info", "--lines", "5"}, {"scoreboard", "--lines=all"}} {
		code, out := execute(t, a, args...)
		if code != 2 || !strings.Contains(out, "unknown flag --lines; it is a flag of `logs` only") || !strings.Contains(out, "valid flags: --host, --fields, --help, -v, -V, --version; commands: ") {
			t.Errorf("args=%v code=%d out=%s", args, code, out)
		}
	}
	code, out := execute(t, a, "discover", "--lines", "5")
	if code != 2 || !strings.Contains(out, "unknown flag --lines; it is a flag of `logs` only") {
		t.Errorf("code=%d out=%s", code, out)
	}
	if len(calls()) != 0 {
		t.Fatalf("requests before usage validation: %v", calls())
	}
}

func TestLogsHelpOffline(t *testing.T) {
	a := New(func(string) string { t.Fatal("offline command read environment"); return "" })
	code, out := execute(t, a, "logs", "--help")
	for _, want := range []string{
		"command: logs\n", "prints no log line without --lines", "lines: \"--lines <n|all>; default prints no log line; the newest n lines, or all lines\"\n", "timeout_s: 15\n",
		"privacy: \"--lines replaces the pool user, the MAC and the Wi-Fi name; only --show-private prints the lines unchanged, and then they can contain the pool user, addresses, hostnames and the Wi-Fi name; other addresses and hostnames are not replaced\"\n",
		"examples[5]: axeos-axi logs --host 192.0.2.10,axeos-axi logs --host 192.0.2.10 --lines 100,axeos-axi logs --host 192.0.2.10 --lines all,axeos-axi logs --host 192.0.2.10 --lines 100 --show-private,axeos-axi logs --host 192.0.2.10 --follow 30\n", "show_private: ",
	} {
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("missing %q in %d %s", want, code, out)
		}
	}
	if strings.Contains(out, "--fields") {
		t.Fatal(out)
	}
}

const privateLogs = "I (10) example: user example-worker connected\n" +
	"I (20) example: fallback example-fallback\n" +
	"I (30) example: ssid example-wifi, mac 02:00:00:00:00:10 again 02:00:00:00:00:10\n" +
	"I (40) example: lower 02:00:00:00:00:10 upper 02:00:00:00:00:AA dash 0A-1B-2C-3D-4E-5F\n" +
	"I (50) example: nothing private\n"

func TestLogsReplacePrivateValues(t *testing.T) {
	host, calls := logsMiner(t, http.StatusOK, "text/plain", privateLogs)
	code, out := execute(t, New(func(string) string { return host }), "logs", "--lines", "all")
	want := "lines[5]{text}:\n" +
		"  \"I (10) example: user <pool-user> connected\"\n" +
		"  \"I (20) example: fallback <pool-user>\"\n" +
		"  \"I (30) example: ssid <wifi-name>, mac <mac> again <mac>\"\n" +
		"  \"I (40) example: lower <mac> upper <mac> dash <mac>\"\n" +
		"  \"I (50) example: nothing private\"\n"
	if code != 0 || !strings.Contains(out, want) {
		t.Fatalf("%d %s", code, out)
	}
	for _, leaked := range []string{"example-worker", "example-fallback", "example-wifi", "02:00", "0A-1B"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("%s leaked in %s", leaked, out)
		}
	}
	if !strings.Contains(out, "--lines all --show-private prints the lines unchanged") {
		t.Fatal(out)
	}
	if strings.Join(calls(), ",") != "GET /api/system/info,GET /api/system/logs" {
		t.Fatal(calls())
	}
}

func TestLogsShowPrivateIsUnchanged(t *testing.T) {
	host, calls := logsMiner(t, http.StatusOK, "text/plain", privateLogs)
	code, out := execute(t, New(func(string) string { return host }), "logs", "--lines", "all", "--show-private")
	for _, kept := range []string{"example-worker", "example-fallback", "example-wifi", "02:00:00:00:00:10", "0A-1B-2C-3D-4E-5F"} {
		if !strings.Contains(out, kept) {
			t.Fatalf("%s missing in %s", kept, out)
		}
	}
	if code != 0 || strings.Contains(out, "<") {
		t.Fatalf("%d %s", code, out)
	}
	if strings.Join(calls(), ",") != "GET /api/system/logs" {
		t.Fatal(calls())
	}
}

func TestLogsInfoFailurePrintsNoLine(t *testing.T) {
	var requests []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		if r.URL.Path == "/api/system/info" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(privateLogs))
	}))
	t.Cleanup(s.Close)
	code, out := execute(t, New(func(string) string { return s.URL }), "logs", "--lines", "all")
	if code != 1 || !strings.Contains(out, "code: miner_read_failed") || strings.Contains(out, "example") {
		t.Fatalf("%d %s", code, out)
	}
	if strings.Join(requests, ",") != "/api/system/info" {
		t.Fatal(requests)
	}
}

func TestLogsShowPrivateNeedsLines(t *testing.T) {
	host, calls := logsMiner(t, http.StatusOK, "text/plain", privateLogs)
	code, out := execute(t, New(func(string) string { return host }), "logs", "--show-private")
	if code != 2 || !strings.Contains(out, "--show-private is valid only together with --lines") {
		t.Fatalf("%d %s", code, out)
	}
	code, out = execute(t, New(func(string) string { return host }), "info", "--show-private")
	if code != 2 || !strings.Contains(out, "unknown flag --show-private; it is a flag of `logs` only") {
		t.Fatalf("%d %s", code, out)
	}
	if len(calls()) != 0 {
		t.Fatal(calls())
	}
}

func TestLogsReportedMacMatchesWithoutRegardToCase(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/system/info" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"macAddr":"aabbccddeeff"}`))
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("id AABBCCDDEEFF and aabbccddeeff\n"))
	}))
	t.Cleanup(s.Close)
	code, out := execute(t, New(func(string) string { return s.URL }), "logs", "--lines", "all")
	if code != 0 || !strings.Contains(out, "\n  id <mac> and <mac>\n") {
		t.Fatalf("%d %s", code, out)
	}
}

func TestLogsReplacesPoolUserOnlyListedInPools(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/system/info" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"stratumUser":"example-worker","fallbackStratumUser":"example-fallback","pools":[{"stratumUser":"example-listed"}]}`))
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("user example-listed connected\n"))
	}))
	t.Cleanup(s.Close)
	code, out := execute(t, New(func(string) string { return s.URL }), "logs", "--lines", "all")
	if code != 0 || !strings.Contains(out, "\n  user <pool-user> connected\n") || strings.Contains(out, "example-listed") {
		t.Fatalf("%d %s", code, out)
	}
}
