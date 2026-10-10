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
