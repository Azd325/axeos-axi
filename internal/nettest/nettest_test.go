package nettest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoopback(t *testing.T) {
	for address, want := range map[string]bool{
		"127.0.0.1:80":      true,
		"[::1]:80":          true,
		"localhost:80":      true,
		"192.0.2.10:80":     false,
		"[2001:db8::10]:80": false,
		"example.org:80":    false,
		"no-port":           false,
	} {
		if got := loopback(address); got != want {
			t.Errorf("loopback(%q) = %v, want %v", address, got, want)
		}
	}
}

func TestGuardedDialRefusesNonLoopbackAndRecords(t *testing.T) {
	mu.Lock()
	violations = nil
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		violations = nil
		mu.Unlock()
	})
	if _, err := guardedDial(context.Background(), "tcp", "192.0.2.10:80"); err == nil {
		t.Fatal("dial to 192.0.2.10 succeeded")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(violations) != 1 {
		t.Fatalf("violations = %v, want one", violations)
	}
}

func TestGuardedDialAllowsHTTPTestServer(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer s.Close()
	client := &http.Client{Transport: &http.Transport{DialContext: guardedDial}}
	resp, err := client.Get(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}
