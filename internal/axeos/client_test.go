package axeos

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHostValidation(t *testing.T) {
	for _, host := range []string{"", "http://", "ftp://192.0.2.10", "http://user:pass@192.0.2.10", "http://192.0.2.10/path", "http://192.0.2.10?", "http://192.0.2.10?q=x", "http://192.0.2.10/path#/"} {
		if _, err := New(host); !errors.Is(err, ErrHost) {
			t.Errorf("accepted %s: %v", host, err)
		}
	}
	for _, host := range []string{"192.0.2.10", "http://192.0.2.10/", "http://192.0.2.10/#/", "192.0.2.10/#/system", "https://example-miner.local:443", "http://[2001:db8::10]:80"} {
		if _, err := New(host); err != nil {
			t.Errorf("rejected %s: %v", host, err)
		}
	}
}

func TestBrowserAddressReachesAPI(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/system/info" {
			t.Errorf("path=%s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"hostname":"example-miner"}`))
	}))
	defer s.Close()
	c, err := New(s.URL + "/#/")
	if err != nil {
		t.Fatal(err)
	}
	if data, err := c.Get(context.Background(), "info"); err != nil || data["hostname"] != "example-miner" {
		t.Fatalf("data=%v error=%v", data, err)
	}
}

func TestProtocolErrorsAreSanitized(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"invalid JSON", "private-identifier", "invalid JSON", 200},
		{"null JSON", "null", "invalid JSON", 200},
		{"bad status", "private-identifier", "HTTP 503", 503},
		{"not found", "private-identifier", "HTTP 404 for info", 404},
		{"oversized", strings.Repeat("x", (4<<20)+1), "4 MiB", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer s.Close()
			c, err := New(s.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Get(context.Background(), "info")
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "private-identifier") || strings.Contains(err.Error(), s.URL) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestRedirectRefused(t *testing.T) {
	called := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer s.Close()
	c, err := New(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Get(context.Background(), "info")
	if err == nil || called {
		t.Fatalf("redirect followed=%t error=%v", called, err)
	}
}

func TestBoundedTimeoutAndCancellation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer s.Close()
	c, err := New(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if c.http.Timeout != 4*time.Second {
		t.Fatal(c.http.Timeout)
	}
	c.http.Timeout = 25 * time.Millisecond
	start := time.Now()
	_, err = c.Get(context.Background(), "info")
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("unbounded timeout: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = c.Get(ctx, "info"); err == nil {
		t.Fatal("ignored cancellation")
	}
	if _, err = c.Get(context.Background(), "reboot"); err == nil {
		t.Fatal("unapproved endpoint")
	}
	if _, err = c.GetList(context.Background(), "info"); err == nil {
		t.Fatal("unapproved list endpoint")
	}
}

func TestListRead(t *testing.T) {
	body := ""
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/system/scoreboard" {
			t.Errorf("request=%s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(body))
	}))
	defer s.Close()
	c, err := New(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body  string
		count int
		fails bool
	}{{`[{"difficulty":5},{"difficulty":3}]`, 2, false}, {"[]", 0, false}, {"null", 0, true}, {`{"difficulty":5}`, 0, true}, {"private-identifier", 0, true}} {
		body = tc.body
		list, err := c.GetList(context.Background(), "scoreboard")
		if (err != nil) != tc.fails || len(list) != tc.count || (err == nil && list == nil) {
			t.Errorf("body=%s list=%v error=%v", tc.body, list, err)
		}
		if err != nil && (!strings.Contains(err.Error(), "invalid JSON") || strings.Contains(err.Error(), "private-identifier")) {
			t.Errorf("error=%v", err)
		}
	}
}

func TestTextRead(t *testing.T) {
	contentType, body := "text/plain", "example-log-line\n"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/system/logs" {
			t.Errorf("request=%s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write([]byte(body))
	}))
	defer s.Close()
	c, err := New(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if text, err := c.GetText(context.Background(), "logs"); err != nil || text != body {
		t.Fatalf("text=%q error=%v", text, err)
	}
	body = ""
	if text, err := c.GetText(context.Background(), "logs"); err != nil || text != "" {
		t.Fatalf("text=%q error=%v", text, err)
	}
	body = "example-log-line\n"
	for _, contentType = range []string{"text/html", "application/json", "", "text/plain; charset"} {
		text, err := c.GetText(context.Background(), "logs")
		if err == nil || text != "" || !strings.Contains(err.Error(), "not plain text") || strings.Contains(err.Error(), "example-log-line") {
			t.Errorf("content type %q: text=%q error=%v", contentType, text, err)
		}
	}
	if _, err = c.GetText(context.Background(), "info"); err == nil {
		t.Fatal("unapproved text endpoint")
	}
}

func TestTextReadTimeout(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer s.Close()
	c, err := New(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if c.logs.Timeout != 15*time.Second || c.http.Timeout != 4*time.Second {
		t.Fatal(c.logs.Timeout, c.http.Timeout)
	}
	c.logs.Timeout = 25 * time.Millisecond
	start := time.Now()
	if _, err = c.GetText(context.Background(), "logs"); err == nil || time.Since(start) > time.Second {
		t.Fatalf("unbounded timeout: %v", err)
	}
}

func TestBodyReadFailureIsNotTheSizeLimit(t *testing.T) {
	stall := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "64")
		_, _ = w.Write([]byte("example-log-line\n"))
		if stall {
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
	}))
	defer s.Close()
	c, err := New(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	c.logs.Timeout = 100 * time.Millisecond
	check := func(name string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "timed out or was interrupted") || strings.Contains(err.Error(), "4 MiB") || strings.Contains(err.Error(), "example-log-line") {
			t.Errorf("%s: error=%v", name, err)
		}
	}
	_, err = c.GetText(context.Background(), "logs")
	check("interrupted text", err)
	_, err = c.Get(context.Background(), "info")
	check("interrupted JSON", err)
	stall = true
	_, err = c.GetText(context.Background(), "logs")
	check("timed out text", err)
}

func TestWriteAllowlistAndUnansweredRequest(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.RequestURI() != "/api/system/restart" {
			t.Errorf("request=%s %s", r.Method, r.URL.RequestURI())
		}
		<-r.Context().Done()
	}))
	defer s.Close()
	c, err := New(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"info", "asic", "statistics", "firmware/checksum", "scoreboard", "logs", "pause", "OTA", "restart?x=1", ""} {
		if err := c.Post(context.Background(), endpoint); err == nil {
			t.Errorf("unapproved write endpoint %q", endpoint)
		}
	}
	if _, err := c.Get(context.Background(), "restart"); err == nil {
		t.Fatal("restart accepted as a read endpoint")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Post(ctx, "restart"); !errors.Is(err, ErrNotSent) {
		t.Fatalf("cancelled before the connection: %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests=%d", requests.Load())
	}
	c.http.Timeout = 25 * time.Millisecond
	start := time.Now()
	if err := c.Post(context.Background(), "restart"); !errors.Is(err, ErrNoAnswer) || time.Since(start) > time.Second {
		t.Fatalf("unanswered request: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests=%d", requests.Load())
	}
}

func TestProxyEnvironmentIgnored(t *testing.T) {
	const proxy = "http://127.0.0.1:1"
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		t.Setenv(name, proxy)
	}
	for _, name := range []string{"NO_PROXY", "no_proxy", "REQUEST_METHOD"} {
		t.Setenv(name, "")
	}
	for _, host := range []string{"http://192.0.2.10", "https://192.0.2.10"} {
		c, err := New(host)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, c.base+"/api/system/restart", nil)
		if err != nil {
			t.Fatal(err)
		}
		for name, client := range map[string]*http.Client{"http": c.http, "logs": c.logs} {
			transport, ok := client.Transport.(*http.Transport)
			if !ok {
				t.Fatalf("%s %s: transport=%T", host, name, client.Transport)
			}
			if transport.Proxy != nil {
				u, err := transport.Proxy(req)
				t.Errorf("%s %s: proxy resolver is set; proxy=%v err=%v", host, name, u, err)
			}
		}
	}
}
