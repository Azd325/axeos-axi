package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMinerReadFailedHintNamesDiscover(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	host := closed.URL
	closed.Close()
	for _, command := range [][]string{{"info"}, {"logs"}, {"health"}, {"firmware"}, {"firmware", "--check-release"}} {
		args := append(command, "--host", host)
		code, out := execute(t, New(noHost), args...)
		if code != 1 || !strings.Contains(out, "code: miner_read_failed\n") || !strings.Contains(out, "help: check --host or AXEOS_HOST and local network connectivity; axeos-axi discover finds miners on the local network\n") {
			t.Fatalf("%v: code=%d\n%s", args, code, out)
		}
	}
}

func TestMinerReadFailedHintHasASeparateTextForAnHTTPStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	t.Cleanup(server.Close)
	code, out := execute(t, New(noHost), "info", "--host", server.URL)
	if code != 1 || !strings.Contains(out, "message: miner returned HTTP 500 for info\n") || !strings.Contains(out, "help: "+readStatusHelp+"\n") || strings.Contains(out, "network connectivity") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if !strings.Contains(readStatusHelp, "axeos-axi discover") {
		t.Fatal(readStatusHelp)
	}
}

func TestWriteFailureHintsStayUnchanged(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	host := closed.URL
	closed.Close()
	code, out := execute(t, New(noHost), "restart", "--host", host, "--confirm")
	if code != 1 || !strings.Contains(out, "help: "+connectivityHelp+"\n") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestDiscoveryFailedHintKeepsTheFlagsOfTheCall(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"discover"}, "then run axeos-axi discover\"\n"},
		{[]string{"discover", "--timeout", "7"}, "then run axeos-axi discover --timeout 7\"\n"},
		{[]string{"discover", "--fields", "hostname,address", "--timeout", "7"}, "then run axeos-axi discover --timeout 7 --fields hostname,address\"\n"},
	} {
		code, out := execute(t, discoverer(t, &fakeBrowser{err: errors.New("no interface")}), tc.args...)
		if code != 1 || !strings.Contains(out, "code: discovery_failed\n") || !strings.HasSuffix(out, tc.want) {
			t.Fatalf("%v: code=%d\n%s", tc.args, code, out)
		}
	}
}

func TestInvalidHostHintKeepsTheCommandOfTheCall(t *testing.T) {
	for command, want := range map[string]string{
		"info":       "help: axeos-axi info --host 192.0.2.10\n",
		"logs":       "help: axeos-axi logs --host 192.0.2.10\n",
		"scoreboard": "help: axeos-axi scoreboard --host 192.0.2.10\n",
	} {
		code, out := execute(t, New(noHost), command, "--host", "http://user:pw@192.0.2.1")
		if code != 2 || !strings.Contains(out, "code: invalid_host\n") || !strings.HasSuffix(out, want) {
			t.Fatalf("%s: code=%d\n%s", command, code, out)
		}
	}
	code, out := execute(t, New(noHost), "--host", "http://user:pw@192.0.2.1")
	if code != 2 || !strings.HasSuffix(out, "help: axeos-axi --host 192.0.2.10\n") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestInvalidHostHintKeepsTheFlagsOfTheCall(t *testing.T) {
	bad := "http://user:pw@192.0.2.1"
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"tuning", "--host", bad, "--frequency", "525", "--core-voltage", "default"}, "help: axeos-axi tuning --host 192.0.2.10 --frequency 525 --core-voltage default\n"},
		{[]string{"tuning", "--frequency", "525", "--host", bad, "--confirm"}, "help: axeos-axi tuning --host 192.0.2.10 --frequency 525 --confirm\n"},
		{[]string{"pool", "--host", bad, "--url", "pool.example.org", "--port", "3333", "--user", "wallet.worker"}, "help: axeos-axi pool --host 192.0.2.10 --url='pool.example.org' --port=3333 --user=<user>\n"},
		{[]string{"logs", "--host", bad, "--follow", "5", "--show-private"}, "help: axeos-axi logs --host 192.0.2.10 --follow 5 --show-private\n"},
		{[]string{"stats", "--host", bad, "--samples", "3", "--columns", "hashrate,power"}, "help: \"axeos-axi stats --host 192.0.2.10 --samples 3 --columns hashrate,power\"\n"},
		{[]string{"firmware", "--host", bad, "--check-release"}, "help: axeos-axi firmware --host 192.0.2.10 --check-release\n"},
		{[]string{"info", "--host", "192.0.2.5", "--host", bad, "--fields", "version"}, "help: axeos-axi info --host '192.0.2.5' --host 192.0.2.10 --fields version\n"},
	} {
		code, out := execute(t, New(noHost), tc.args...)
		if code != 2 || !strings.Contains(out, "code: invalid_host\n") || !strings.HasSuffix(out, tc.want) || strings.Contains(out, "wallet.worker") || strings.Contains(out, "user:pw") {
			t.Fatalf("%v: code=%d\n%s", tc.args, code, out)
		}
	}
}

func TestHostSaveHintHasASeparateTextForAnHTTPStatus(t *testing.T) {
	configHome(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	t.Cleanup(server.Close)
	code, out := execute(t, New(noHost), "host", "save", server.URL)
	if code != 1 || !strings.Contains(out, "code: miner_read_failed\n") || !strings.Contains(out, "answered with an HTTP error status") || strings.Contains(out, "network connectivity") || !strings.Contains(out, "axeos-axi discover") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestSavedHostHintHasASeparateTextForAnHTTPStatus(t *testing.T) {
	path := configHome(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	t.Cleanup(server.Close)
	saveFile(t, path, server.URL+"\n")
	code, out := execute(t, New(noHost), "info")
	if code != 1 || !strings.Contains(out, "help: "+savedReadStatusHelp+"\n") || strings.Contains(out, "AXEOS_HOST") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}
