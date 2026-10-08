package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

const (
	severalRestartHead     = "request: POST /api/system/restart\n"
	severalPoolHead        = "request: PATCH /api/system\n"
	restartEffectOfSeveral = "effect: each miner restarts and stops hashing until it is up again; a restart has no command that reverses it\n"
	poolVerifyFields       = " --fields stratumURL,stratumPort,fallbackStratumURL,fallbackStratumPort shows the URL and port of each pool of each miner that the miner reports; stratumUser and fallbackStratumUser show the users"
	restoreHelp            = "restore has the command that sets the previous values of that one miner again"
)

var fallbackPortWrite = sentPools(sentPool(fallbackPool, "fallback.example.org", 4444, "example-worker"))

func fallbackPortAt(port float64) func(map[string]any, []map[string]any) {
	return func(_ map[string]any, pools []map[string]any) { pools[1]["stratumPort"] = port }
}

func hostList(hosts ...string) string {
	quoted := make([]string, len(hosts))
	for i, h := range hosts {
		quoted[i] = "--host '" + h + "'"
	}
	return strings.Join(quoted, " ")
}

func onHosts(command []string, hosts ...string) []string {
	return append(slices.Clone(command), hostArgs(hosts...)...)
}

func closedPort(t *testing.T) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	host := s.URL
	s.Close()
	return host
}

func minerGoneAfterTheCheck(t *testing.T) (string, func() string) {
	t.Helper()
	info := fixture(t, "info")
	var mu sync.Mutex
	var requests []string
	var s *httptest.Server
	s = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		w.Header().Set("Connection", "close")
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(info); err != nil {
			t.Error(err)
		}
		_ = s.Listener.Close()
	}))
	t.Cleanup(s.Close)
	return s.URL, func() string { mu.Lock(); defer mu.Unlock(); return strings.Join(requests, ",") }
}

func TestPoolPreviewOfSeveralMinersSendsNoWrite(t *testing.T) {
	first, firstCalls := poolMiner(t, nil, settingsSaved)
	second, secondCalls := poolMiner(t, fallbackPortAt(4444), settingsSaved)
	third, thirdCalls := poolMiner(t, nil, settingsSaved)
	a := New(noHost)

	code, out := execute(t, a, onHosts([]string{"pool", "--fallback-port", "4444"}, first, second)...)
	want := severalPoolHead + "sent: false\ncount: 2\nfailed: 0\n" +
		fmt.Sprintf("miners[2]{host,fallback_port_present,fallback_port_new,changes,error}:\n  %q,3333,4444,true,null\n  %q,4444,4444,false,null\n", first, second) +
		"effect: " + poolEffects + "\n" +
		"execute: \"axeos-axi pool " + hostList(first, second) + " --fallback-port=4444 --confirm\"\n"
	if code != 0 || out != want {
		t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
	}

	code, out = execute(t, a, onHosts([]string{"pool", "--url", "new.example.org", "--user", "other-worker"}, first, second, third)...)
	row := ",pool.example.org,new.example.org,set,set,true,null\n"
	want = severalPoolHead + "sent: false\ncount: 3\nfailed: 0\n" +
		fmt.Sprintf("miners[3]{host,url_present,url_new,user_present,user_new,changes,error}:\n  %q"+row+"  %q"+row+"  %q"+row, first, second, third) +
		poolPrivate + "effect: " + poolEffects + "\n" +
		"execute: \"axeos-axi pool " + hostList(first, second, third) + " --url='new.example.org' --user=<user> --show-user --confirm\"\n" +
		"help[1]: \"a confirmed change of a pool user needs --show-user, so the execute command has it; replace <user> with the new user\"\n"
	if code != 0 || out != want {
		t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
	}
	if firstCalls() != poolRead+","+poolRead || secondCalls() != poolRead+","+poolRead || thirdCalls() != poolRead {
		t.Fatalf("requests: %s | %s | %s", firstCalls(), secondCalls(), thirdCalls())
	}
}

func TestRestartPreviewOfSeveralMinersSendsNoRestart(t *testing.T) {
	first, firstCalls := poolMiner(t, nil, settingsSaved)
	second, secondCalls := poolMiner(t, func(info map[string]any, _ []map[string]any) { info["uptimeSeconds"] = float64(90) }, settingsSaved)
	third, thirdCalls := poolMiner(t, func(info map[string]any, _ []map[string]any) { delete(info, "uptimeSeconds") }, settingsSaved)
	a := New(noHost)

	code, out := execute(t, a, onHosts([]string{"restart"}, first, second)...)
	want := severalRestartHead + "sent: false\ncount: 2\nfailed: 0\n" +
		fmt.Sprintf("miners[2]{host,uptime_s,changes,error}:\n  %q,612447,true,null\n  %q,90,true,null\n", first, second) +
		restartEffectOfSeveral +
		"execute: \"axeos-axi restart " + hostList(first, second) + " --confirm\"\n"
	if code != 0 || out != want {
		t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
	}

	code, out = execute(t, a, onHosts([]string{"restart"}, third, first, second)...)
	want = severalRestartHead + "sent: false\ncount: 3\nfailed: 0\n" +
		fmt.Sprintf("miners[3]{host,uptime_s,changes,error}:\n  %q,null,true,null\n  %q,612447,true,null\n  %q,90,true,null\n", third, first, second) +
		restartEffectOfSeveral +
		"execute: \"axeos-axi restart " + hostList(third, first, second) + " --confirm\"\n"
	if code != 0 || out != want {
		t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
	}
	if firstCalls() != poolRead+","+poolRead || secondCalls() != poolRead+","+poolRead || thirdCalls() != poolRead {
		t.Fatalf("requests: %s | %s | %s", firstCalls(), secondCalls(), thirdCalls())
	}
}

func TestSeveralMinersGetNoWriteWhenOneMinerFailsTheCheck(t *testing.T) {
	type command struct {
		name, head, columns, good, empty, inspect string
		args                                      []string
	}
	restartCommand := command{"restart", severalRestartHead, "uptime_s", "612447", "null", "", []string{"restart"}}
	poolCommand := command{"pool", severalPoolHead, "fallback_port_present,fallback_port_new", "3333,4444", "null,null", " --fallback-port=4444", []string{"pool", "--fallback-port", "4444"}}
	for _, tc := range []struct {
		name, code string
		command    command
		broken     func(t *testing.T) string
	}{
		{"restart, a miner that does not answer", "miner_read_failed", restartCommand, closedPort},
		{"pool, a miner that does not answer", "miner_read_failed", poolCommand, closedPort},
		{"restart, a miner that answers HTTP 500", "miner_read_failed", restartCommand, func(t *testing.T) string {
			host, _ := statusMiner(t, http.StatusInternalServerError)
			return host
		}},
		{"pool, a miner without a pools list", "not_supported", poolCommand, func(t *testing.T) string {
			host, _ := poolMiner(t, func(info map[string]any, _ []map[string]any) { delete(info, "pools") }, settingsSaved)
			return host
		}},
		{"pool, a miner whose present port cannot be set again", "not_reversible", poolCommand, func(t *testing.T) string {
			host, _ := poolMiner(t, fallbackPortAt(0), settingsSaved)
			return host
		}},
	} {
		for _, confirm := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, confirm %v", tc.name, confirm), func(t *testing.T) {
				first, firstCalls := poolMiner(t, nil, settingsSaved)
				broken := tc.broken(t)
				third, thirdCalls := poolMiner(t, nil, settingsSaved)
				c := tc.command
				args := onHosts(c.args, first, broken, third)
				inspect := "axeos-axi info --host '" + broken + "'"
				if c.name == "pool" {
					inspect = "axeos-axi pool --host '" + broken + "'" + c.inspect
				}
				inspect = "\"" + inspect + " for the error message of that miner\""
				want := c.head + "sent: false\ncount: 3\nfailed: 1\n"
				if confirm {
					args = append(args, "--confirm")
					untouched := ",not_attempted,"
					if c.name == "pool" {
						untouched += "null,"
					}
					want += "changed: 0\n" +
						"miners[3]{host," + c.columns + ",result," + map[string]string{"restart": "", "pool": "restore,"}[c.name] + "error}:\n" +
						fmt.Sprintf("  %q,%s%snull\n  %q,%s%s%s\n  %q,%s%snull\n", first, c.good, untouched, broken, c.empty, untouched, tc.code, third, c.good, untouched) +
						"result: \"1 of 3 miners failed the check before the write, so no write request was sent to any miner\"\n" +
						"help[1]: " + inspect + "\n"
				} else {
					want += "miners[3]{host," + c.columns + ",changes,error}:\n" +
						fmt.Sprintf("  %q,%s,true,null\n  %q,%s,null,%s\n  %q,%s,true,null\n", first, c.good, broken, c.empty, tc.code, third, c.good)
					if c.name == "pool" {
						want += "effect: " + poolEffects + "\n"
					} else {
						want += restartEffectOfSeveral
					}
					want += "execute: \"axeos-axi " + c.name + " " + hostList(first, broken, third) + c.inspect + " --confirm\"\n" +
						"help[2]: " + inspect + ",the execute command sends no write request while a miner fails the check\n"
				}
				code, out := execute(t, New(noHost), args...)
				if code != 1 || out != want {
					t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
				}
				if firstCalls() != poolRead || thirdCalls() != poolRead {
					t.Fatalf("requests: %s | %s", firstCalls(), thirdCalls())
				}
			})
		}
	}
}

func TestSeveralMinersWriteStopsAtTheFirstFailedWrite(t *testing.T) {
	failing := restartAnswer(http.StatusInternalServerError, "text/plain", "private-identifier")
	t.Run("pool", func(t *testing.T) {
		first, firstCalls := poolMiner(t, nil, settingsSaved)
		second, secondCalls := poolMiner(t, nil, failing)
		third, thirdCalls := poolMiner(t, nil, settingsSaved)
		code, out := execute(t, New(noHost), onHosts([]string{"pool", "--fallback-port", "4444", "--confirm"}, first, second, third)...)
		restore := "\"axeos-axi pool --host '%s' --fallback-port=3333 --confirm\""
		want := severalPoolHead + "sent: true\ncount: 3\nfailed: 1\nchanged: 1\n" +
			"miners[3]{host,fallback_port_present,fallback_port_new,result,restore,error}:\n" +
			fmt.Sprintf("  %q,3333,4444,changed,"+restore+",null\n  %q,3333,4444,failed,"+restore+",pool_failed\n  %q,3333,4444,not_attempted,null,null\n", first, first, second, second, third) +
			"result: \"the write to miner 2 of 3 failed: the request was sent; miner returned HTTP 500 for settings; the call stopped with 1 changed before it and 1 not attempted after it\"\n" +
			"note: " + staleReadNote + "\n" +
			"help[2]: \"axeos-axi info " + hostList(first, second, third) + poolVerifyFields + "; read them before another pool call\"," +
			restoreHelp + "\n"
		if code != 1 || out != want {
			t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
		}
		if firstCalls() != poolRead+","+fallbackPortWrite || secondCalls() != poolRead+","+fallbackPortWrite || thirdCalls() != poolRead {
			t.Fatalf("requests: %s | %s | %s", firstCalls(), secondCalls(), thirdCalls())
		}
	})
	t.Run("restart", func(t *testing.T) {
		first, firstCalls := poolMiner(t, nil, settingsSaved)
		second, secondCalls := poolMiner(t, nil, failing)
		third, thirdCalls := poolMiner(t, nil, settingsSaved)
		code, out := execute(t, New(noHost), onHosts([]string{"restart", "--confirm"}, first, second, third)...)
		want := severalRestartHead + "sent: true\ncount: 3\nfailed: 1\nchanged: 1\n" +
			fmt.Sprintf("miners[3]{host,uptime_s,result,error}:\n  %q,612447,changed,null\n  %q,612447,failed,restart_failed\n  %q,612447,not_attempted,null\n", first, second, third) +
			"result: \"the write to miner 2 of 3 failed: the request was sent; miner returned HTTP 500 for restart; the call stopped with 1 changed before it and 1 not attempted after it\"\n" +
			"help[1]: \"axeos-axi info " + hostList(first, second, third) + " --fields uptime_s,reset_reason shows uptime_s and reset_reason of each miner; read them before another restart call\"\n"
		if code != 1 || out != want {
			t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
		}
		if firstCalls() != poolRead+","+restartCall || secondCalls() != poolRead+","+restartCall || thirdCalls() != poolRead {
			t.Fatalf("requests: %s | %s | %s", firstCalls(), secondCalls(), thirdCalls())
		}
	})
}

func TestSeveralMinersWriteThatWasNotSentHasNoRestoreCommand(t *testing.T) {
	gone, goneCalls := minerGoneAfterTheCheck(t)
	last, lastCalls := poolMiner(t, nil, settingsSaved)
	code, out := execute(t, New(noHost), onHosts([]string{"pool", "--fallback-port", "4444", "--confirm"}, gone, last)...)
	want := severalPoolHead + "sent: false\ncount: 2\nfailed: 1\nchanged: 0\n" +
		"miners[2]{host,fallback_port_present,fallback_port_new,result,restore,error}:\n" +
		fmt.Sprintf("  %q,3333,4444,failed,null,pool_not_sent\n  %q,3333,4444,not_attempted,null,null\n", gone, last) +
		"result: \"the write to miner 1 of 2 failed: miner unreachable or request timed out; the request was not sent; the call stopped with 0 changed before it and 1 not attempted after it\"\n" +
		"help[1]: \"axeos-axi info " + hostList(gone, last) + poolVerifyFields + "; read them before another pool call\"\n"
	if code != 1 || out != want {
		t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
	}
	if goneCalls() != poolRead || lastCalls() != poolRead {
		t.Fatalf("requests: %s | %s", goneCalls(), lastCalls())
	}
}

func TestSeveralMinersWriteSendsARequestToAMinerAtTheNewValue(t *testing.T) {
	first, firstCalls := poolMiner(t, nil, settingsSaved)
	second, secondCalls := poolMiner(t, fallbackPortAt(4444), settingsSaved)
	third, thirdCalls := poolMiner(t, fallbackPortAt(4444), settingsSaved)
	a := New(noHost)

	code, out := execute(t, a, onHosts([]string{"pool", "--fallback-port", "4444", "--confirm"}, second, first)...)
	want := severalPoolHead + "sent: true\ncount: 2\nfailed: 0\nchanged: 2\n" +
		"miners[2]{host,fallback_port_present,fallback_port_new,result,restore,error}:\n" +
		fmt.Sprintf("  %q,4444,4444,changed,\"axeos-axi pool --host '%s' --fallback-port=4444 --confirm\",null\n  %q,3333,4444,changed,\"axeos-axi pool --host '%s' --fallback-port=3333 --confirm\",null\n", second, second, first, first) +
		"result: each miner accepted the request; " + poolEffects + "\n" +
		"note: " + staleReadNote + "\n" +
		"help[3]: \"axeos-axi info " + hostList(second, first) + poolVerifyFields + "\"," +
		"\"axeos-axi restart " + hostList(second, first) + " for a preview of the restart that makes each changed miner use the stored values\"," + restoreHelp + "\n"
	if code != 0 || out != want {
		t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
	}

	code, out = execute(t, a, onHosts([]string{"pool", "--fallback-port", "4444", "--confirm"}, second, third)...)
	want = severalPoolHead + "sent: true\ncount: 2\nfailed: 0\nchanged: 2\n" +
		"miners[2]{host,fallback_port_present,fallback_port_new,result,restore,error}:\n" +
		fmt.Sprintf("  %q,4444,4444,changed,\"axeos-axi pool --host '%s' --fallback-port=4444 --confirm\",null\n  %q,4444,4444,changed,\"axeos-axi pool --host '%s' --fallback-port=4444 --confirm\",null\n", second, second, third, third) +
		"result: each miner accepted the request; " + poolEffects + "\n" +
		"note: " + staleReadNote + "\n" +
		"help[3]: \"axeos-axi info " + hostList(second, third) + poolVerifyFields + "\"," +
		"\"axeos-axi restart " + hostList(second, third) + " for a preview of the restart that makes each changed miner use the stored values\"," + restoreHelp + "\n"
	if code != 0 || out != want {
		t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
	}
	if firstCalls() != poolRead+","+fallbackPortWrite || secondCalls() != poolRead+","+fallbackPortWrite+","+poolRead+","+fallbackPortWrite || thirdCalls() != poolRead+","+fallbackPortWrite {
		t.Fatalf("requests: %s | %s | %s", firstCalls(), secondCalls(), thirdCalls())
	}
}

func TestSeveralMinersWriteSendsOneRequestToEachMinerInTheOrderOfTheFlags(t *testing.T) {
	t.Run("restart", func(t *testing.T) {
		var mu sync.Mutex
		var order []string
		ordered := func(name string) http.HandlerFunc {
			return func(w http.ResponseWriter, _ *http.Request) {
				mu.Lock()
				order = append(order, name)
				mu.Unlock()
				w.WriteHeader(http.StatusOK)
			}
		}
		first, firstCalls := poolMiner(t, nil, ordered("first"))
		second, secondCalls := poolMiner(t, nil, ordered("second"))
		third, thirdCalls := poolMiner(t, nil, ordered("third"))
		code, out := execute(t, New(noHost), onHosts([]string{"restart", "--confirm"}, third, first, second)...)
		want := severalRestartHead + "sent: true\ncount: 3\nfailed: 0\nchanged: 3\n" +
			fmt.Sprintf("miners[3]{host,uptime_s,result,error}:\n  %q,612447,changed,null\n  %q,612447,changed,null\n  %q,612447,changed,null\n", third, first, second) +
			"result: each miner accepted the restart and stops hashing until it is up again; a restart has no command that reverses it\n" +
			"help[1]: \"axeos-axi info " + hostList(third, first, second) + " --fields uptime_s,reset_reason shows uptime_s and reset_reason of each miner when it is up again\"\n"
		if code != 0 || out != want {
			t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
		}
		for _, calls := range []string{firstCalls(), secondCalls(), thirdCalls()} {
			if calls != poolRead+","+restartCall {
				t.Fatalf("requests: %s", calls)
			}
		}
		if strings.Join(order, ",") != "third,first,second" {
			t.Fatalf("order of the writes: %v", order)
		}
	})
	t.Run("pool with a user that is printed", func(t *testing.T) {
		first, firstCalls := poolMiner(t, nil, settingsSaved)
		second, secondCalls := poolMiner(t, func(_ map[string]any, pools []map[string]any) { pools[0]["stratumUser"] = "second-worker" }, settingsSaved)
		code, out := execute(t, New(noHost), onHosts([]string{"pool", "--user", "other-worker", "--port", "4444", "--show-user", "--confirm"}, first, second)...)
		restore := "\"axeos-axi pool --host '%s' --port=3333 --user='%s' --show-user --confirm\""
		want := severalPoolHead + "sent: true\ncount: 2\nfailed: 0\nchanged: 2\n" +
			"miners[2]{host,port_present,port_new,user_present,user_new,result,restore,error}:\n" +
			fmt.Sprintf("  %q,3333,4444,example-worker,other-worker,changed,"+restore+",null\n  %q,3333,4444,second-worker,other-worker,changed,"+restore+",null\n", first, first, "example-worker", second, second, "second-worker") +
			"result: each miner accepted the request; " + poolEffects + "\n" +
			"note: " + staleReadNote + "\n" +
			"help[3]: \"axeos-axi info " + hostList(first, second) + poolVerifyFields + "\"," +
			"\"axeos-axi restart " + hostList(first, second) + " for a preview of the restart that makes each changed miner use the stored values\"," + restoreHelp + "\n"
		if code != 0 || out != want {
			t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
		}
		write := sentPools(sentPool(primaryPool, "pool.example.org", 4444, "other-worker"))
		if firstCalls() != poolRead+","+write || secondCalls() != poolRead+","+write {
			t.Fatalf("requests: %s | %s", firstCalls(), secondCalls())
		}
	})
}

func TestSeveralMinersWritePrintsNoUserWithoutShowUser(t *testing.T) {
	first, _ := poolMiner(t, nil, settingsSaved)
	second, _ := poolMiner(t, nil, settingsSaved)
	a := New(noHost)
	for _, args := range [][]string{
		{"pool", "--user", "other-worker", "--fallback-url", "other.example.org"},
		{"pool", "--fallback-url", "other.example.org", "--confirm"},
	} {
		code, out := execute(t, a, onHosts(args, first, second)...)
		if code != 0 || strings.Contains(out, "example-worker") || strings.Contains(out, "other-worker") {
			t.Fatalf("args=%v code=%d\n%s", args, code, out)
		}
	}
	code, out := execute(t, a, onHosts([]string{"pool", "--user", "other-worker", "--confirm"}, first, second)...)
	if code != 2 || !strings.Contains(out, "--user with --confirm requires --show-user") || strings.Contains(out, "other-worker") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestSameHostTwiceOnAWriteSendsNothing(t *testing.T) {
	first, firstCalls := poolMiner(t, nil, settingsSaved)
	second, secondCalls := poolMiner(t, nil, settingsSaved)
	a := New(noHost)
	for _, args := range [][]string{
		{"restart", "--host", first, "--host", first},
		{"restart", "--confirm", "--host", first, "--host", second, "--host", strings.ToUpper(first) + "/"},
		{"pool", "--fallback-port", "4444", "--host", first, "--host", strings.TrimPrefix(first, "http://")},
		{"pool", "--fallback-port", "4444", "--confirm", "--host", second, "--host", first, "--host", second},
	} {
		code, out := execute(t, a, args...)
		if code != 2 || !strings.Contains(out, "code: usage") || !strings.Contains(out, "more than once") || !strings.Contains(out, "are the same miner") {
			t.Errorf("args=%v code=%d out=%s", args, code, out)
		}
	}
	if firstCalls() != "" || secondCalls() != "" {
		t.Fatalf("requests: %s | %s", firstCalls(), secondCalls())
	}
}

func TestOneHostOnAWriteIsUnchanged(t *testing.T) {
	host, calls := poolMiner(t, nil, settingsSaved)
	a := New(func(string) string { return "http://invalid.example" })
	restartPreview := "host: \"" + host + "\"\nrequest: POST /api/system/restart\nsent: false\neffect: the miner restarts and stops hashing until it is up again\nexecute: \"axeos-axi restart --host '" + host + "' --confirm\"\n"
	restartSent := "host: \"" + host + "\"\nrequest: POST /api/system/restart\nsent: true\nresult: the miner accepted the restart; it stops hashing until it is up again\nhelp[1]: \"axeos-axi info --host '" + host + "' shows uptime_s and reset_reason when the miner is up again\"\n"
	body := printedBody(printedRow(fallbackPool, "fallback.example.org", 4444, "<not printed>"))
	rows := "settings[1]{setting,present,new,changes}:\n  fallback_port,3333,4444,true\n"
	poolPreview := "host: \"" + host + "\"\n" + body + "sent: false\n" + rows + poolPrivate + "effect: " + poolEffects + "\nexecute: \"axeos-axi pool --host '" + host + "' --fallback-port=4444 --confirm\"\n"
	poolSent := "host: \"" + host + "\"\n" + body + "sent: true\n" + rows + poolPrivate + "result: the miner accepted the request; " + poolEffects + "\n" +
		"note: " + staleReadNote + "\n" +
		"help[3]: \"axeos-axi info --host '" + host + "' --fields stratumURL,stratumPort,fallbackStratumURL,fallbackStratumPort shows the URL and port of each pool that the miner reports; stratumUser and fallbackStratumUser show the users\"," +
		"\"axeos-axi restart --host '" + host + "' for a preview of the restart that makes the miner use the stored values\"," +
		"\"axeos-axi pool --host '" + host + "' --fallback-port=3333 --confirm sets the previous values again\"\n"
	for _, tc := range []struct {
		want string
		args []string
	}{
		{restartPreview, []string{"restart", "--host", host}},
		{restartSent, []string{"restart", "--host", host, "--confirm"}},
		{poolPreview, []string{"pool", "--host", host, "--fallback-port", "4444"}},
		{poolSent, []string{"pool", "--host", host, "--fallback-port", "4444", "--confirm"}},
	} {
		code, out := execute(t, a, tc.args...)
		if code != 0 || out != tc.want {
			t.Fatalf("args=%v code=%d\n%s\nwant\n%s", tc.args, code, out, tc.want)
		}
	}
	if calls() != restartCall+","+poolRead+","+poolRead+","+fallbackPortWrite {
		t.Fatalf("requests=%s", calls())
	}
}

func TestSeveralMinersWriteJSON(t *testing.T) {
	first, _ := poolMiner(t, nil, settingsSaved)
	second, _ := poolMiner(t, fallbackPortAt(4444), settingsSaved)
	a := New(noHost)
	code, out := execute(t, a, onHosts([]string{"pool", "--json", "--fallback-port", "4444"}, first, second)...)
	want := fmt.Sprintf(`{"request":"PATCH /api/system","sent":false,"count":2,"failed":0,"miners":[{"host":%q,"fallback_port_present":3333,"fallback_port_new":4444,"changes":true,"error":null},{"host":%q,"fallback_port_present":4444,"fallback_port_new":4444,"changes":false,"error":null}],"effect":%q,"execute":"axeos-axi pool %s --fallback-port=4444 --confirm"}`+"\n", first, second, poolEffects, hostList(first, second))
	if code != 0 || out != want {
		t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
	}
	code, out = execute(t, a, onHosts([]string{"pool", "--json", "--fallback-port", "4444", "--confirm"}, first, second)...)
	if code != 0 || !strings.Contains(out, fmt.Sprintf(`"miners":[{"host":%q,"fallback_port_present":3333,"fallback_port_new":4444,"result":"changed","restore":"axeos-axi pool --host '%s' --fallback-port=3333 --confirm","error":null},{"host":%q,"fallback_port_present":4444,"fallback_port_new":4444,"result":"changed","restore":"axeos-axi pool --host '%s' --fallback-port=4444 --confirm","error":null}]`, first, first, second, second)) {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestNoFlagReadsTheHostsOfAWriteFromDiscover(t *testing.T) {
	a := New(noHost)
	a.Browser = &fakeBrowser{services: advertisedMiners()}
	for _, args := range [][]string{
		{"restart", "--all"}, {"restart", "--discover", "--confirm"}, {"restart", "--discovered"}, {"pool", "--all", "--fallback-port", "4444"}, {"pool", "--fallback-port", "4444", "--timeout", "1"},
	} {
		code, out := execute(t, a, args...)
		if code != 2 || !strings.Contains(out, "code: usage") {
			t.Errorf("args=%v code=%d out=%s", args, code, out)
		}
	}
	for _, command := range []string{"restart", "pool"} {
		if flags := validFlags(command); strings.Contains(flags, "discover") || strings.Contains(flags, "--all") {
			t.Errorf("%s flags: %s", command, flags)
		}
	}
}

func TestWriteHelpStatesTheRulesForSeveralMiners(t *testing.T) {
	a := New(noHost)
	for command, want := range map[string][]string{
		"restart": {"repeat --host to change several miners in one call", "several_miners: \"with --host given more than once: each call first sends one GET /api/system/info to each miner", "the result is changed, failed or not_attempted", "a restart has no command that reverses it", "no flag takes the hosts from discover"},
		"pool":    {"repeat --host to change several miners in one call", "several_miners: \"with --host given more than once: each call first sends one GET /api/system/info to each miner", "the result is changed, failed or not_attempted", "restore is the complete command that sets the previous values of that one miner again", "no flag takes the hosts from discover"},
		"tuning":  {"--host more than once is a usage error, because tuning for several miners is not supported yet"},
	} {
		code, out := execute(t, a, command, "--help")
		for _, text := range want {
			if code != 0 || !strings.Contains(out, text) {
				t.Errorf("%s --help lacks %q\n%s", command, text, out)
			}
		}
	}
	if _, out := execute(t, a, "tuning", "--help"); strings.Contains(out, "several_miners") {
		t.Errorf("tuning --help: %s", out)
	}
}
