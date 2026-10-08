package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strings"
	"sync"
	"testing"
	"time"
)

const releasePath = "/repos/bitaxeorg/ESP-Miner/releases/latest"

type fakeGitHub struct {
	url  string
	mu   sync.Mutex
	dump []string
}

func (g *fakeGitHub) requests() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.dump...)
}

func githubFake(t *testing.T, handler http.HandlerFunc) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dump, err := httputil.DumpRequest(r, true)
		if err != nil {
			t.Error(err)
		}
		g.mu.Lock()
		g.dump = append(g.dump, string(dump))
		g.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(s.Close)
	g.url = s.URL + releasePath
	return g
}

func latestRelease(tag string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": tag, "name": "Release " + tag, "published_at": "2026-09-20T15:57:20Z",
			"html_url": "https://github.com/bitaxeorg/ESP-Miner/releases/tag/" + tag, "prerelease": false,
		})
	}
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

func releaseApp(g *fakeGitHub) *App {
	a := New(noHost)
	a.Version = "9.9.9"
	a.releaseURL = g.url
	return a
}

func minerWithVersion(t *testing.T, version any) (string, func() []string) {
	info := fixture(t, "info")
	info["version"] = version
	return miner(t, info)
}

func minerWithoutChecksum(t *testing.T, version string) (string, func() []string) {
	t.Helper()
	info := fixture(t, "info")
	info["version"] = version
	var mu sync.Mutex
	var requests []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.URL.Path != "/api/system/info" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(info)
	}))
	t.Cleanup(s.Close)
	return s.URL, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), requests...) }
}

func TestCheckReleaseSendsTheCompleteRequest(t *testing.T) {
	g := githubFake(t, latestRelease("v2.15.3"))
	host, calls := minerWithVersion(t, "v2.15.3")
	code, out := execute(t, releaseApp(g), "firmware", "--check-release", "--host", host)
	if code != 0 {
		t.Fatalf("code=%d\n%s", code, out)
	}
	got := g.requests()
	if len(got) != 1 {
		t.Fatalf("%d release requests: %q", len(got), got)
	}
	want := "GET " + releasePath + " HTTP/1.1\r\nHost: " + strings.TrimSuffix(strings.TrimPrefix(g.url, "http://"), releasePath) + "\r\nAccept: application/vnd.github+json\r\nAccept-Encoding: gzip\r\nUser-Agent: axeos-axi/9.9.9\r\n\r\n"
	if got[0] != want {
		t.Fatalf("request\n%q\nwant\n%q", got[0], want)
	}
	if strings.Contains(got[0], strings.TrimPrefix(host, "http://")) || strings.Contains(got[0], "miner-a") {
		t.Fatalf("the request carries a value of the miner: %q", got[0])
	}
	if c := calls(); len(c) != 2 || c[0] != "GET /api/system/info" || c[1] != "GET /api/system/firmware/checksum" {
		t.Fatalf("miner requests %v", c)
	}
}

func TestCheckReleaseComparisons(t *testing.T) {
	for _, tc := range []struct{ miner, tag, comparison, reason string }{
		{"v2.15.3", "v2.15.3", "up_to_date", ""},
		{"v2.15.2", "v2.15.3", "update_available", ""},
		{"v2.16.0", "v2.15.3", "newer_than_release", ""},
		{"v2.15.2rc0", "v2.15.3", "unknown", "reason: \"the miner version \\\"v2.15.2rc0\\\" is not in the form vMAJOR.MINOR.PATCH\"\n"},
		{"v2.15.2rc0-30-gabc1234", "v2.15.3", "unknown", "reason: \"the miner version \\\"v2.15.2rc0-30-gabc1234\\\" is not in the form vMAJOR.MINOR.PATCH\"\n"},
		{"v2.15.3-dirty", "v2.15.3", "unknown", "reason: \"the miner version \\\"v2.15.3-dirty\\\" is not in the form vMAJOR.MINOR.PATCH\"\n"},
		{"Unknown", "v2.15.3", "unknown", "reason: \"the miner version \\\"Unknown\\\" is not in the form vMAJOR.MINOR.PATCH\"\n"},
		{"v2.15.3", "nightly", "unknown", "reason: \"the release tag \\\"nightly\\\" is not in the form vMAJOR.MINOR.PATCH\"\n"},
	} {
		t.Run(tc.miner+" vs "+tc.tag, func(t *testing.T) {
			g := githubFake(t, latestRelease(tc.tag))
			host, _ := minerWithVersion(t, tc.miner)
			code, out := execute(t, releaseApp(g), "firmware", "--check-release", "--host", host)
			want := "miner_version: " + tc.miner + "\nrelease:\n  tag: " + tc.tag + "\n  name: Release " + tc.tag + "\n  date: \"2026-09-20T15:57:20Z\"\n  url: \"https://github.com/bitaxeorg/ESP-Miner/releases/tag/" + tc.tag + "\"\ncomparison: " + tc.comparison + "\n" + tc.reason +
				"partition: ota_1\nversion: v2.15.3\nsize_bytes: 1638400\nsha256: 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08\n"
			if code != 0 || out != want {
				t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
			}
		})
	}
}

func TestCheckReleaseMinerWithoutVersion(t *testing.T) {
	g := githubFake(t, latestRelease("v2.15.3"))
	host, _ := minerWithVersion(t, nil)
	code, out := execute(t, releaseApp(g), "firmware", "--check-release", "--host", host)
	if code != 0 || !strings.Contains(out, "miner_version: null\n") || !strings.Contains(out, "comparison: unknown\nreason: the miner did not report a firmware version\n") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestCheckReleaseWithoutChecksumPath(t *testing.T) {
	g := githubFake(t, latestRelease("v2.15.3"))
	host, calls := minerWithoutChecksum(t, "v2.15.2")
	code, out := execute(t, releaseApp(g), "firmware", "--check-release", "--host", host)
	want := "miner_version: v2.15.2\nrelease:\n  tag: v2.15.3\n  name: Release v2.15.3\n  date: \"2026-09-20T15:57:20Z\"\n  url: \"https://github.com/bitaxeorg/ESP-Miner/releases/tag/v2.15.3\"\ncomparison: update_available\nchecksum: not supported by this firmware\n"
	if code != 0 || out != want {
		t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
	}
	if len(g.requests()) != 1 || len(calls()) != 2 {
		t.Fatalf("requests %v %v", g.requests(), calls())
	}
	code, out = execute(t, releaseApp(g), "firmware", "--host", host)
	if code != 1 || !strings.Contains(out, "code: not_supported") {
		t.Fatalf("firmware without the flag: code=%d\n%s", code, out)
	}
}

func TestCheckReleaseJSON(t *testing.T) {
	g := githubFake(t, latestRelease("v2.15.3"))
	host, _ := minerWithVersion(t, "v2.15.2")
	code, out := execute(t, releaseApp(g), "firmware", "--check-release", "--json", "--host", host)
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	release, _ := doc["release"].(map[string]any)
	if code != 0 || doc["miner_version"] != "v2.15.2" || doc["comparison"] != "update_available" || release["tag"] != "v2.15.3" || release["date"] != "2026-09-20T15:57:20Z" || release["url"] == nil || doc["sha256"] == nil {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestCheckReleaseReadErrors(t *testing.T) {
	gone := httptest.NewServer(http.NotFoundHandler())
	goneURL := gone.URL + releasePath
	gone.Close()
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		message string
	}{
		{"rate limit", status(http.StatusForbidden), "rate limit"},
		{"too many requests", status(http.StatusTooManyRequests), "rate limit"},
		{"server error", status(http.StatusInternalServerError), "HTTP 500"},
		{"bad gateway", status(http.StatusBadGateway), "HTTP 502"},
		{"not found", status(http.StatusNotFound), "HTTP 404"},
		{"no tag", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"name":"x"}`)) }, "no release tag"},
		{"not json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`<html>`)) }, "not a release document"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := githubFake(t, tc.handler)
			host, _ := minerWithVersion(t, "v2.15.3")
			code, out := execute(t, releaseApp(g), "firmware", "--check-release", "--host", host)
			if code != 1 || !strings.HasPrefix(out, "miner_version: v2.15.3\nerror:\n  code: release_read_failed\n  message: ") || !strings.Contains(out, tc.message) || !strings.Contains(out, "\nhelp: ") {
				t.Fatalf("code=%d\n%s", code, out)
			}
			if strings.Contains(out, "comparison") {
				t.Fatalf("a failed read prints a comparison\n%s", out)
			}
		})
	}
	t.Run("no network", func(t *testing.T) {
		a := releaseApp(&fakeGitHub{url: goneURL})
		host, _ := minerWithVersion(t, "v2.15.3")
		code, out := execute(t, a, "firmware", "--check-release", "--host", host)
		if code != 1 || !strings.Contains(out, "miner_version: v2.15.3\nerror:\n  code: release_read_failed\n  message: GitHub is unreachable") {
			t.Fatalf("code=%d\n%s", code, out)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		g := githubFake(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
		host, _ := minerWithVersion(t, "v2.15.3")
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		var b strings.Builder
		code := releaseApp(g).Run(ctx, []string{"firmware", "--check-release", "--host", host}, &b)
		if code != 1 || !strings.Contains(b.String(), "code: release_read_failed") {
			t.Fatalf("code=%d\n%s", code, b.String())
		}
	})
}

func TestCheckReleaseFollowsNoRedirect(t *testing.T) {
	other := githubFake(t, latestRelease("v9.9.9"))
	g := githubFake(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.url, http.StatusMovedPermanently)
	})
	host, _ := minerWithVersion(t, "v2.15.3")
	code, out := execute(t, releaseApp(g), "firmware", "--check-release", "--host", host)
	if code != 1 || !strings.Contains(out, "code: release_read_failed") || !strings.Contains(out, "HTTP 301") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if len(other.requests()) != 0 {
		t.Fatalf("the redirect target received %q", other.requests())
	}
}

func TestCheckReleaseMinerErrors(t *testing.T) {
	g := githubFake(t, latestRelease("v2.15.3"))
	down, _ := statusMiner(t, http.StatusInternalServerError)
	code, out := execute(t, releaseApp(g), "firmware", "--check-release", "--host", down)
	if code != 1 || !strings.HasPrefix(out, "error:\n  code: miner_read_failed\n") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	infoOnly := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/system/info" {
			_ = json.NewEncoder(w).Encode(fixture(t, "info"))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(infoOnly.Close)
	code, out = execute(t, releaseApp(g), "firmware", "--check-release", "--host", infoOnly.URL)
	if code != 1 || !strings.HasPrefix(out, "miner_version: v2.15.3\nerror:\n  code: miner_read_failed\n") {
		t.Fatalf("checksum read failure: code=%d\n%s", code, out)
	}
	if n := len(g.requests()); n != 0 {
		t.Fatalf("a failed miner read sent %d release requests", n)
	}
}

func TestNoOutsideRequestWithoutTheFlag(t *testing.T) {
	g := githubFake(t, latestRelease("v2.15.3"))
	host, calls := minerWithVersion(t, "v2.15.3")
	for _, args := range [][]string{{"firmware"}, {"info"}, {"firmware", "--json"}} {
		if code, out := execute(t, releaseApp(g), append(args, "--host", host)...); code != 0 {
			t.Fatalf("%v: code=%d\n%s", args, code, out)
		}
	}
	if n := g.requests(); len(n) != 0 {
		t.Fatalf("a command without --check-release sent %q", n)
	}
	for _, c := range calls() {
		if c != "GET /api/system/info" && c != "GET /api/system/firmware/checksum" {
			t.Fatalf("unexpected miner request %s", c)
		}
	}
}

func TestCheckReleaseIsRefusedOnOtherCommands(t *testing.T) {
	g := githubFake(t, latestRelease("v2.15.3"))
	host, calls := minerWithVersion(t, "v2.15.3")
	for _, command := range []string{"info", "asic", "stats", "scoreboard", "logs", "health", "discover", "restart", "tuning", "pool", "skill", ""} {
		args := []string{"--check-release", "--host", host}
		if command != "" {
			args = append([]string{command}, args...)
		}
		code, out := execute(t, releaseApp(g), args...)
		if code != 2 || !strings.Contains(out, "message: unknown flag --check-release; it is a flag of `firmware` only\n") {
			t.Errorf("%q: code=%d\n%s", command, code, out)
		}
	}
	if len(g.requests()) != 0 || len(calls()) != 0 {
		t.Fatalf("requests %v %v", g.requests(), calls())
	}
}

func TestCheckReleaseTakesOneMiner(t *testing.T) {
	g := githubFake(t, latestRelease("v2.15.3"))
	first, firstCalls := minerWithVersion(t, "v2.15.3")
	second, secondCalls := minerWithVersion(t, "v2.15.3")
	code, out := execute(t, releaseApp(g), "firmware", "--check-release", "--host", first, "--host", second)
	if code != 2 || !strings.Contains(out, "code: usage") || !strings.Contains(out, "--check-release takes one miner") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if len(g.requests()) != 0 || len(firstCalls()) != 0 || len(secondCalls()) != 0 {
		t.Fatal("a request was sent")
	}
	code, out = execute(t, releaseApp(g), "firmware", "--check-release", "--fields", "version", "--host", first)
	if code != 2 || !strings.Contains(out, "--fields cannot be combined with --check-release") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if len(g.requests()) != 0 || len(firstCalls()) != 0 {
		t.Fatal("a request was sent")
	}
}
