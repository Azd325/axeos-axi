package release

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDefaultAddressIsFixed(t *testing.T) {
	if Latest != "https://api.github.com/repos/bitaxeorg/ESP-Miner/releases/latest" {
		t.Fatalf("release address changed: %s", Latest)
	}
	if got := New("", "1.0.0").url; got != Latest {
		t.Fatalf("default client address %s", got)
	}
}

func TestCompare(t *testing.T) {
	for _, tc := range []struct{ miner, tag, want string }{
		{"v2.15.3", "v2.15.3", UpToDate},
		{"v2.15.2", "v2.15.3", UpdateAvailable},
		{"v2.9.9", "v2.10.0", UpdateAvailable},
		{"v1.99.99", "v2.0.0", UpdateAvailable},
		{"v2.16.0", "v2.15.3", NewerThanRelease},
		{"v2.10.0", "v2.9.9", NewerThanRelease},
		{"v3.0.0", "v2.99.99", NewerThanRelease},
		{"v2.15.2rc0", "v2.15.3", Unknown},
		{"v2.15.2rc0-30-gabc1234", "v2.15.3", Unknown},
		{"v2.15.3-dirty", "v2.15.3", Unknown},
		{"Unknown", "v2.15.3", Unknown},
		{"", "v2.15.3", Unknown},
		{"2.15.3", "v2.15.3", Unknown},
		{"v02.15.3", "v2.15.3", Unknown},
		{"v2.15", "v2.15.3", Unknown},
		{"v2.15.3", "v2.15.4-beta", Unknown},
		{"v2.15.3", "latest", Unknown},
		{"v99999999999999999999.0.0", "v2.15.3", Unknown},
	} {
		got, reason := Compare(tc.miner, tc.tag)
		if got != tc.want || (got == Unknown) != (reason != "") {
			t.Errorf("Compare(%q, %q) = %s, %q; want %s", tc.miner, tc.tag, got, reason, tc.want)
		}
	}
}

func TestLatestSeparatesRateLimitFromRefusal(t *testing.T) {
	for _, tc := range []struct {
		name      string
		code      int
		remaining string
		want      error
	}{
		{"429", http.StatusTooManyRequests, "", ErrRateLimit},
		{"403 with no requests left", http.StatusForbidden, "0", ErrRateLimit},
		{"403 with requests left", http.StatusForbidden, "5", ErrRefused},
		{"403 without the header", http.StatusForbidden, "", ErrRefused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.remaining != "" {
					w.Header().Set("X-RateLimit-Remaining", tc.remaining)
				}
				w.WriteHeader(tc.code)
			}))
			defer s.Close()
			if _, err := New(s.URL, "1").Latest(context.Background()); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
		})
	}
}
