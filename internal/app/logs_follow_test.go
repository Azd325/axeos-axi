package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azd325/axeos-axi/internal/ws/wstest"
)

type streamMiner struct {
	url   string
	mu    sync.Mutex
	calls []string
}

func (m *streamMiner) requests() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.Join(m.calls, ",")
}

func newStreamMiner(t *testing.T, stream func(w http.ResponseWriter, r *http.Request)) *streamMiner {
	t.Helper()
	m := &streamMiner{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.calls = append(m.calls, r.Method+" "+r.URL.Path)
		m.mu.Unlock()
		if r.URL.Path == "/api/system/info" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(infoFixture)
			return
		}
		if r.URL.Path != "/api/ws" {
			http.NotFound(w, r)
			return
		}
		stream(w, r)
	}))
	t.Cleanup(s.Close)
	m.url = s.URL
	return m
}

func shortFollow(t *testing.T) {
	t.Helper()
	old := followUnit
	followUnit = 50 * time.Millisecond
	t.Cleanup(func() { followUnit = old })
}

func TestFollowPrintsLinesAndEndsAtTimeLimit(t *testing.T) {
	shortFollow(t)
	m := newStreamMiner(t, func(w http.ResponseWriter, r *http.Request) {
		s := wstest.Upgrade(w, r, "")
		defer func() { _ = s.Conn.Close() }()
		s.Text("I (10) example: user example-worker connected\n")
		s.Text("I (20) example: ssid example-wifi mac 02:00:00:00:00:10\n")
		s.Text("\n")
		time.Sleep(time.Second)
	})
	code, out := execute(t, New(func(string) string { return m.url }), "logs", "--follow", "2")
	want := "follow_limit_s: 2\n" +
		"line_1: \"I (10) example: user <pool-user> connected\"\n" +
		"line_2: \"I (20) example: ssid <wifi-name> mac <mac>\"\n" +
		"lines: 2\n"
	if code != 0 || !strings.HasPrefix(out, want) || !strings.Contains(out, "ended: time_limit\n") || !strings.Contains(out, "private: ") {
		t.Fatalf("%d\n%s", code, out)
	}
	for _, leaked := range []string{"example-worker", "example-wifi", "02:00"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("%s leaked in %s", leaked, out)
		}
	}
	if m.requests() != "GET /api/system/info,GET /api/ws" {
		t.Fatal(m.requests())
	}
}

func TestFollowShowPrivateIsUnchangedAndReadsNoInfo(t *testing.T) {
	shortFollow(t)
	m := newStreamMiner(t, func(w http.ResponseWriter, r *http.Request) {
		s := wstest.Upgrade(w, r, "")
		defer func() { _ = s.Conn.Close() }()
		s.Text("user example-worker\n")
		s.Send(8, true, nil)
	})
	code, out := execute(t, New(func(string) string { return m.url }), "logs", "--follow", "5", "--show-private")
	if code != 0 || !strings.Contains(out, "line_1: user example-worker\n") || strings.Contains(out, "private:") || !strings.Contains(out, "ended: closed_by_miner\n") {
		t.Fatalf("%d\n%s", code, out)
	}
	if m.requests() != "GET /api/ws" {
		t.Fatal(m.requests())
	}
}

func TestFollowJoinsLinesAcrossFrames(t *testing.T) {
	m := newStreamMiner(t, func(w http.ResponseWriter, r *http.Request) {
		s := wstest.Upgrade(w, r, "")
		defer func() { _ = s.Conn.Close() }()
		s.Text("I (1) user exam")
		s.Text("ple-worker\nI (2) two\nI (3) tail")
		s.Send(8, true, nil)
	})
	code, out := execute(t, New(func(string) string { return m.url }), "logs", "--follow", "5")
	want := "line_1: I (1) user <pool-user>\nline_2: I (2) two\nline_3: I (3) tail\nlines: 3\n"
	if code != 0 || !strings.Contains(out, want) {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestFollowZeroLinesIsDefinite(t *testing.T) {
	shortFollow(t)
	m := newStreamMiner(t, func(w http.ResponseWriter, r *http.Request) {
		s := wstest.Upgrade(w, r, "")
		defer func() { _ = s.Conn.Close() }()
		time.Sleep(time.Second)
	})
	code, out := execute(t, New(func(string) string { return m.url }), "logs", "--follow", "1")
	if code != 0 || !strings.Contains(out, "lines: 0\n") || !strings.Contains(out, "ended: time_limit\n") || !strings.Contains(out, "state: 0 new log lines arrived while following\n") || strings.Contains(out, "error") {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestFollowInterruptEndsCleanly(t *testing.T) {
	m := newStreamMiner(t, func(w http.ResponseWriter, r *http.Request) {
		s := wstest.Upgrade(w, r, "")
		defer func() { _ = s.Conn.Close() }()
		s.Text("one\n")
		op, _, _ := s.ReadFrame()
		if op != 8 {
			t.Errorf("client sent opcode %d, not a close frame", op)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	var b bytes.Buffer
	code := New(func(string) string { return m.url }).Run(ctx, []string{"logs", "--follow", "60", "--show-private"}, &b)
	if code != 0 || !strings.Contains(b.String(), "line_1: one\nlines: 1\n") || !strings.Contains(b.String(), "ended: interrupted\n") {
		t.Fatalf("%d\n%s", code, b.String())
	}
}

func TestFollowInterruptDuringHandshakeEndsCleanly(t *testing.T) {
	release := make(chan struct{})
	m := newStreamMiner(t, func(w http.ResponseWriter, r *http.Request) { <-release })
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	var b bytes.Buffer
	code := New(func(string) string { return m.url }).Run(ctx, []string{"logs", "--follow", "60", "--show-private"}, &b)
	if code != 0 || !strings.Contains(b.String(), "lines: 0\n") || !strings.Contains(b.String(), "ended: interrupted\n") || strings.Contains(b.String(), "miner_read_failed") {
		t.Fatalf("%d\n%s", code, b.String())
	}
}

func TestFollowBrokenConnectionPrintsLinesThenError(t *testing.T) {
	m := newStreamMiner(t, func(w http.ResponseWriter, r *http.Request) {
		s := wstest.Upgrade(w, r, "")
		s.Text("one\n")
		s.Text("two\n")
		_ = s.Conn.Close()
	})
	code, out := execute(t, New(func(string) string { return m.url }), "logs", "--follow", "5", "--show-private")
	if code != 1 || !strings.Contains(out, "line_1: one\nline_2: two\nlines: 2\n") || !strings.Contains(out, "code: connection_lost") || !strings.Contains(out, "help: ") || strings.Contains(out, "ended:") {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestFollowProtocolFault(t *testing.T) {
	m := newStreamMiner(t, func(w http.ResponseWriter, r *http.Request) {
		s := wstest.Upgrade(w, r, "")
		defer func() { _ = s.Conn.Close() }()
		s.Text("one\n")
		_, _ = s.Conn.Write([]byte{0x81, 0x81, 1, 2, 3, 4, 'a' ^ 1})
		time.Sleep(200 * time.Millisecond)
	})
	code, out := execute(t, New(func(string) string { return m.url }), "logs", "--follow", "5", "--show-private")
	if code != 1 || !strings.Contains(out, "line_1: one\nlines: 1\n") || !strings.Contains(out, "code: protocol_error") || !strings.Contains(out, "masked frame from a server") {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestFollowOpenErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		code    string
	}{
		{"429", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Max WebSocket clients reached", http.StatusTooManyRequests)
		}, "connections_full"},
		{"404", http.NotFound, "not_supported"},
		{"302 to root", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/", http.StatusFound) }, "not_supported"},
		{"401", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "Unauthorized", http.StatusUnauthorized) }, "access_refused"},
		{"500", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", http.StatusInternalServerError) }, "handshake_failed"},
		{"wrong accept key", func(w http.ResponseWriter, r *http.Request) { _ = wstest.Upgrade(w, r, "AAAA") }, "handshake_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newStreamMiner(t, tc.handler)
			code, out := execute(t, New(func(string) string { return m.url }), "logs", "--follow", "5", "--show-private")
			if code != 1 || !strings.HasPrefix(out, "error:\n  code: "+tc.code+"\n") || !strings.Contains(out, "\nhelp: ") || strings.Contains(out, "line_") {
				t.Fatalf("%d\n%s", code, out)
			}
			if m.requests() != "GET /api/ws" {
				t.Fatal(m.requests())
			}
		})
	}
}

func TestFollowUnreachableAndInfoFailure(t *testing.T) {
	var mu sync.Mutex
	var streamHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/ws" {
			mu.Lock()
			streamHits++
			mu.Unlock()
		}
		http.Error(w, "x", http.StatusInternalServerError)
	}))
	defer srv.Close()
	code, out := execute(t, New(func(string) string { return srv.URL }), "logs", "--follow", "5")
	if code != 1 || !strings.Contains(out, "code: miner_read_failed") {
		t.Fatalf("%d\n%s", code, out)
	}
	code, out = execute(t, New(func(string) string { return "http://127.0.0.1:1" }), "logs", "--follow", "5", "--show-private")
	if code != 1 || !strings.Contains(out, "code: miner_read_failed") {
		t.Fatalf("%d\n%s", code, out)
	}
	mu.Lock()
	defer mu.Unlock()
	if streamHits != 0 {
		t.Fatalf("%d stream requests after the info read failed", streamHits)
	}
}

func TestFollowUsageErrorsMakeNoConnection(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"logs", "--follow", "0"}, "--follow requires whole seconds from 1 to 300; 300 seconds is the largest time limit"},
		{[]string{"logs", "--follow", "301"}, "--follow requires whole seconds from 1 to 300"},
		{[]string{"logs", "--follow", "1.5"}, "--follow requires whole seconds from 1 to 300"},
		{[]string{"logs", "--follow", "abc"}, "--follow requires whole seconds from 1 to 300"},
		{[]string{"logs", "--follow"}, "--follow requires a value"},
		{[]string{"logs", "--follow", "5", "--follow", "6"}, "--follow was given more than once"},
		{[]string{"logs", "--follow", "5", "--lines", "10"}, "--follow cannot be combined with --lines"},
		{[]string{"logs", "--lines", "all", "--follow", "5"}, "--follow cannot be combined with --lines"},
		{[]string{"logs", "--follow", "5", "--fields", "text"}, "unknown flag --fields for `logs`"},
		{[]string{"info", "--follow", "5"}, "unknown flag --follow; it is a flag of `logs` only"},
		{[]string{"--follow", "5"}, "unknown flag --follow; it is a flag of `logs` only"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			m := newStreamMiner(t, func(w http.ResponseWriter, r *http.Request) {})
			code, out := execute(t, New(func(string) string { return m.url }), tc.args...)
			if code != 2 || !strings.Contains(out, tc.want) || !strings.Contains(out, "--follow") || m.requests() != "" {
				t.Fatalf("%d\n%s\n%s", code, out, m.requests())
			}
		})
	}
}

func TestFollowHelpOffline(t *testing.T) {
	code, out := execute(t, New(func(string) string { return "http://127.0.0.1:1" }), "logs", "--help")
	for _, want := range []string{"--follow <seconds>", "follow_max_s: 300", "line_1", "1 of the 10 WebSocket places", "ended"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
}
