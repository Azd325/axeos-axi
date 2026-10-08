package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const restartCall = "POST /api/system/restart"

func restartMiner(t *testing.T, answer http.HandlerFunc) (string, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var requests []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) != 0 {
			t.Errorf("request body=%q error=%v", body, err)
		}
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		answer(w, r)
	}))
	t.Cleanup(s.Close)
	return s.URL, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), requests...) }
}

func restartAnswer(status int, contentType, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func accepted() http.HandlerFunc {
	return restartAnswer(http.StatusOK, "application/json", `{"message":"System will restart shortly."}`)
}

func TestRestartWithoutConfirmSendsNothing(t *testing.T) {
	host, calls := restartMiner(t, accepted())
	want := "host: \"" + host + "\"\nrequest: POST /api/system/restart\nsent: false\neffect: the miner restarts and stops hashing until it is up again\nexecute: \"axeos-axi restart --host '" + host + "' --confirm\"\n"
	for _, tc := range []struct {
		env  string
		args []string
	}{
		{host, []string{"restart"}},
		{"http://invalid.example", []string{"restart", "--host", host}},
		{"http://invalid.example", []string{"--host=" + host, "restart"}},
	} {
		code, out := execute(t, New(func(string) string { return tc.env }), tc.args...)
		if code != 0 || out != want {
			t.Errorf("args=%v code=%d\n%q\nwant\n%q", tc.args, code, out, want)
		}
	}
	if len(calls()) != 0 {
		t.Fatalf("requests without --confirm: %v", calls())
	}
}

func TestRestartWithConfirmSendsOneRequest(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer http.HandlerFunc
		args   []string
	}{
		{"JSON answer", accepted(), []string{"restart", "--confirm"}},
		{"flag first", accepted(), []string{"--confirm", "restart"}},
		{"plain text answer before v2.13.0", restartAnswer(http.StatusOK, "text/plain", "System will restart shortly."), []string{"restart", "--confirm"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host, calls := restartMiner(t, tc.answer)
			code, out := execute(t, New(func(string) string { return host }), tc.args...)
			want := "host: \"" + host + "\"\nrequest: POST /api/system/restart\nsent: true\nresult: the miner accepted the restart; it stops hashing until it is up again\nhelp[1]: \"axeos-axi info --host '" + host + "' shows uptime_s and reset_reason when the miner is up again\"\n"
			if code != 0 || out != want {
				t.Fatalf("code=%d\n%q\nwant\n%q", code, out, want)
			}
			if strings.Join(calls(), ",") != restartCall {
				t.Fatalf("requests=%v", calls())
			}
		})
	}
}

func TestRestartFailuresStateWhetherTheRequestWasSent(t *testing.T) {
	followed := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed = true }))
	defer target.Close()
	for _, tc := range []struct {
		name, want string
		answer     http.HandlerFunc
	}{
		{"outside the allowed network", "code: restart_failed\n  message: the request was sent; miner returned HTTP 401 for restart; the miner did not confirm a restart\n", restartAnswer(http.StatusUnauthorized, "text/plain", "Unauthorized")},
		{"server error", "code: restart_failed\n  message: the request was sent; miner returned HTTP 500 for restart; the miner did not confirm a restart\n", restartAnswer(http.StatusInternalServerError, "text/plain", "private-identifier")},
		{"not found", "code: restart_failed\n  message: the request was sent; miner returned HTTP 404 for restart; the miner did not confirm a restart\n", restartAnswer(http.StatusNotFound, "text/plain", "")},
		{"redirect that keeps the method", "code: restart_failed\n  message: the request was sent; miner returned HTTP 307 for restart; the miner did not confirm a restart\n", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+"/api/system/restart", http.StatusTemporaryRedirect)
		}},
		{"root redirect", "code: restart_failed\n  message: the request was sent; miner returned HTTP 302 for restart; the miner did not confirm a restart\n", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/", http.StatusFound)
		}},
		{"closed connection", "code: restart_unconfirmed\n  message: \"the request was sent, but the miner closed the connection or did not answer in time; the restart is unconfirmed\"\n", func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host, calls := restartMiner(t, tc.answer)
			code, out := execute(t, New(func(string) string { return host }), "restart", "--confirm")
			if code != 1 || !strings.HasPrefix(out, "error:\n  "+tc.want+"help: \"axeos-axi info --host '"+host+"' shows uptime_s and reset_reason") || strings.Contains(out, "private-identifier") {
				t.Fatalf("code=%d\n%s", code, out)
			}
			if strings.Join(calls(), ",") != restartCall {
				t.Fatalf("requests=%v", calls())
			}
		})
	}
	if followed {
		t.Fatal("redirect followed")
	}
}

func TestRestartUnreachableMinerIsNotSent(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	host := s.URL
	s.Close()
	code, out := execute(t, New(func(string) string { return host }), "restart", "--confirm")
	if code != 1 || !strings.HasPrefix(out, "error:\n  code: restart_not_sent\n  message: miner unreachable or request timed out; the request was not sent\nhelp: ") || strings.Contains(out, host) {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestRestartRejectsInputBeforeNetwork(t *testing.T) {
	host, calls := restartMiner(t, accepted())
	a := New(func(string) string { return host })
	for _, args := range [][]string{
		{"restart", "--fields", "host"}, {"restart", "--confirm", "--fields", "host"}, {"restart", "--lines", "1"}, {"restart", "--timeout", "1"},
		{"restart", "--force"}, {"restart", "--confrim"}, {"restart", "--confirm", "--yes"}, {"restart", "--confirm", "--yaml"},
		{"restart", "--confirm=true"}, {"restart", "--confirm", "true"}, {"restart", "--confirm", "now"}, {"restart", "--confirm", "restart"},
		{"restart", "--confirm", "--host"}, {"restart", "--host", "--confirm"}, {"restart", "--confirm", "--help", "--bad"},
		{"--confirm"}, {"info", "--confirm"}, {"logs", "--confirm"},
	} {
		code, out := execute(t, a, args...)
		if code != 2 || !strings.Contains(out, "valid flags:") {
			t.Errorf("args=%v code=%d out=%s", args, code, out)
		}
	}
	code, out := execute(t, a, "restart", "--fields", "host")
	if code != 2 || !strings.Contains(out, "unknown flag --fields for `restart`") || !strings.Contains(out, "valid flags: --host, --confirm, --json, --help, -v, -V, --version;") {
		t.Fatalf("%d %s", code, out)
	}
	code, out = execute(t, New(func(string) string { return "" }), "restart", "--confirm")
	if code != 2 || !strings.Contains(out, "code: host_required") {
		t.Fatalf("%d %s", code, out)
	}
	code, out = execute(t, a, "restart", "--confirm", "--host", "http://192.0.2.10/path")
	if code != 2 || !strings.Contains(out, "code: invalid_host") {
		t.Fatalf("%d %s", code, out)
	}
	if len(calls()) != 0 {
		t.Fatalf("requests before usage validation: %v", calls())
	}
}

func TestRestartWithRepeatedHostSendsNothing(t *testing.T) {
	first, firstCalls := restartMiner(t, accepted())
	second, secondCalls := restartMiner(t, accepted())
	a := New(func(string) string { return "" })
	for _, args := range [][]string{
		{"restart", "--host", first, "--host", second, "--confirm"},
		{"restart", "--host=" + first, "--host=" + second, "--confirm"},
		{"restart", "--confirm", "--host", first, "--host=" + second},
		{"restart", "--host", first, "--host", second},
	} {
		code, out := execute(t, a, args...)
		if code != 2 || !strings.Contains(out, "code: usage") || !strings.Contains(out, "--host was given more than once; `restart` takes one miner; several miners are accepted by the home view") || !strings.Contains(out, "valid flags: --host, --confirm,") {
			t.Errorf("args=%v code=%d out=%s", args, code, out)
		}
	}
	if len(firstCalls()) != 0 || len(secondCalls()) != 0 {
		t.Fatalf("requests after a repeated --host: %v %v", firstCalls(), secondCalls())
	}
}

func TestRestartHelpAndVersionSendNothing(t *testing.T) {
	a := New(func(string) string { t.Fatal("offline command read environment"); return "" })
	a.Version = "1.2.3"
	for _, args := range [][]string{{"restart", "--help"}, {"restart", "--confirm", "--help"}, {"--help", "--confirm", "restart"}} {
		code, out := execute(t, a, args...)
		if code != 0 || !strings.HasPrefix(out, "command: restart\ndescription: \"Changes the miner: ") || !strings.Contains(out, "confirm: \"--confirm; ") || !strings.Contains(out, "examples[3]:") || strings.Contains(out, "--fields") {
			t.Fatalf("args=%v code=%d\n%s", args, code, out)
		}
	}
	if code, out := execute(t, a, "restart", "--confirm", "--version"); code != 0 || out != "1.2.3\n" {
		t.Fatalf("%d %s", code, out)
	}
}

func TestHomeNamesRestart(t *testing.T) {
	host, calls := miner(t, fixture(t, "info"))
	code, out := execute(t, New(func(string) string { return host }))
	if code != 0 || !strings.Contains(out, "\"axeos-axi restart --host '"+host+"' for a preview of a miner restart; it sends no request without --confirm\"") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if strings.Join(calls(), ",") != "GET /api/system/info" {
		t.Fatalf("requests=%v", calls())
	}
}
