package axeos

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHostValidation(t *testing.T) {
	for _, host := range []string{"", "http://", "ftp://192.0.2.10", "http://user:pass@192.0.2.10", "http://192.0.2.10/path", "http://192.0.2.10?", "http://192.0.2.10?q=x", "http://192.0.2.10/#fragment"} {
		if _, err := New(host); !errors.Is(err, ErrHost) {
			t.Errorf("accepted %s: %v", host, err)
		}
	}
	for _, host := range []string{"192.0.2.10", "http://192.0.2.10/", "https://example-miner.local:443", "http://[2001:db8::10]:80"} {
		if _, err := New(host); err != nil {
			t.Errorf("rejected %s: %v", host, err)
		}
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
}
