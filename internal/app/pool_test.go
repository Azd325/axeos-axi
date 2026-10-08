package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const (
	poolRead      = "GET /api/system/info"
	poolColumns   = "{id,stratumCert,stratumDecodeCoinbase,stratumExtranonceSubscribe,stratumPassword,stratumPort,stratumProtocol,stratumSuggestedDifficulty,stratumTLS,stratumURL,stratumUser,stratumV2AuthorityPubkey,stratumV2ChannelType,stratumV2RequireAuth}"
	staleReadNote = "firmware v2.15.3 reports the pool values from before this write on the next read; the stored values are the new ones; a second pool call directly after this one sends the old complete record and sets this change back"
	poolEffects   = "the miner stores the values at once and keeps them across a restart; an open pool connection uses the old values until the miner restarts or connects again"
	poolPrivate   = "private: the pool user is not printed; --show-user prints it\n"
	primaryPool   = "primary"
	fallbackPool  = "fallback"
)

func poolMiner(t *testing.T, change func(info map[string]any, pools []map[string]any), answer http.HandlerFunc) (string, func() string) {
	t.Helper()
	info := fixture(t, "info")
	if change != nil {
		list := info["pools"].([]any)
		change(info, []map[string]any{list[0].(map[string]any), list[1].(map[string]any)})
	}
	var mu sync.Mutex
	var requests []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		mu.Lock()
		requests = append(requests, strings.TrimSpace(r.Method+" "+r.URL.RequestURI()+" "+string(body)))
		mu.Unlock()
		switch r.Method + " " + r.URL.RequestURI() {
		case poolRead:
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(info); err != nil {
				t.Error(err)
			}
		case "PATCH /api/system":
			if r.Header.Get("Content-Type") != "application/json" || r.ContentLength != int64(len(body)) {
				t.Errorf("content type=%q length=%d body=%q", r.Header.Get("Content-Type"), r.ContentLength, body)
			}
			answer(w, r)
		case restartCall:
			answer(w, r)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s.URL, func() string { mu.Lock(); defer mu.Unlock(); return strings.Join(requests, ",") }
}

func sentPool(role, url string, port int, user string) string {
	id := map[string]string{primaryPool: "0", fallbackPool: "1"}[role]
	return `{"id":` + id + `,"stratumCert":"x","stratumDecodeCoinbase":true,"stratumExtranonceSubscribe":false,"stratumPassword":"*****","stratumPort":` + strconv.Itoa(port) +
		`,"stratumProtocol":"SV1","stratumSuggestedDifficulty":1000,"stratumTLS":0,"stratumURL":"` + url + `","stratumUser":"` + user + `","stratumV2AuthorityPubkey":"","stratumV2ChannelType":"extended","stratumV2RequireAuth":false}`
}

func sentPools(records ...string) string {
	return `PATCH /api/system {"pools":[` + strings.Join(records, ",") + `]}`
}

func printedRow(role, url string, port int, user string) string {
	id := map[string]string{primaryPool: "0", fallbackPool: "1"}[role]
	return "    " + id + ",<not printed>,true,false,*****," + strconv.Itoa(port) + ",SV1,1000,0," + url + "," + user + ",\"\",extended,false\n"
}

func printedBody(rows ...string) string {
	return "request: PATCH /api/system\nbody:\n  pools[" + strconv.Itoa(len(rows)) + "]" + poolColumns + ":\n" + strings.Join(rows, "")
}

type poolCase struct {
	name, body, rows, private, command, revert, write string
	hiddenUser                                        bool
	args                                              []string
}

var poolCases = []poolCase{
	{
		name:    "primary URL",
		body:    printedBody(printedRow(primaryPool, "new.example.org", 3333, "<not printed>")),
		rows:    "settings[1]{setting,present,new,changes}:\n  url,pool.example.org,new.example.org,true\n",
		private: poolPrivate,
		command: "--url='new.example.org'",
		revert:  "--url='pool.example.org'",
		write:   sentPools(sentPool(primaryPool, "new.example.org", 3333, "example-worker")),
		args:    []string{"pool", "--url", "new.example.org"},
	},
	{
		name:    "port of each pool",
		body:    printedBody(printedRow(primaryPool, "pool.example.org", 4444, "<not printed>"), printedRow(fallbackPool, "fallback.example.org", 5555, "<not printed>")),
		rows:    "settings[2]{setting,present,new,changes}:\n  port,3333,4444,true\n  fallback_port,3333,5555,true\n",
		private: poolPrivate,
		command: "--port=4444 --fallback-port=5555",
		revert:  "--port=3333 --fallback-port=3333",
		write:   sentPools(sentPool(primaryPool, "pool.example.org", 4444, "example-worker"), sentPool(fallbackPool, "fallback.example.org", 5555, "example-worker")),
		args:    []string{"--fallback-port=5555", "pool", "--port", "4444"},
	},
	{
		name:       "user that is not printed",
		body:       printedBody(printedRow(primaryPool, "pool.example.org", 3333, "<not printed>")),
		rows:       "settings[1]{setting,present,new,changes}:\n  user,set,set,true\n",
		private:    poolPrivate,
		command:    "--user=<user> --show-user",
		write:      sentPools(sentPool(primaryPool, "pool.example.org", 3333, "other-worker")),
		hiddenUser: true,
		args:       []string{"pool", "--user", "other-worker"},
	},
	{
		name:    "user that is printed",
		body:    printedBody(printedRow(fallbackPool, "fallback.example.org", 3333, "other-worker")),
		rows:    "settings[1]{setting,present,new,changes}:\n  fallback_user,example-worker,other-worker,true\n",
		command: "--fallback-user='other-worker' --show-user",
		revert:  "--fallback-user='example-worker' --show-user",
		write:   sentPools(sentPool(fallbackPool, "fallback.example.org", 3333, "other-worker")),
		args:    []string{"pool", "--show-user", "--fallback-user=other-worker"},
	},
	{
		name:    "one of two values changes",
		body:    printedBody(printedRow(fallbackPool, "fallback.example.org", 4444, "<not printed>")),
		rows:    "settings[2]{setting,present,new,changes}:\n  fallback_url,fallback.example.org,fallback.example.org,false\n  fallback_port,3333,4444,true\n",
		private: poolPrivate,
		command: "--fallback-url='fallback.example.org' --fallback-port=4444",
		revert:  "--fallback-url='fallback.example.org' --fallback-port=3333",
		write:   sentPools(sentPool(fallbackPool, "fallback.example.org", 4444, "example-worker")),
		args:    []string{"pool", "--fallback-port", "4444", "--fallback-url", "fallback.example.org"},
	},
	{
		name:    "pool with no changed value is in the body",
		body:    printedBody(printedRow(primaryPool, "pool.example.org", 3333, "example-worker"), printedRow(fallbackPool, "other.example.org", 3333, "example-worker")),
		rows:    "settings[3]{setting,present,new,changes}:\n  url,pool.example.org,pool.example.org,false\n  user,example-worker,example-worker,false\n  fallback_url,fallback.example.org,other.example.org,true\n",
		command: "--url='pool.example.org' --user='example-worker' --fallback-url='other.example.org' --show-user",
		revert:  "--url='pool.example.org' --user='example-worker' --fallback-url='fallback.example.org' --show-user",
		write:   sentPools(sentPool(primaryPool, "pool.example.org", 3333, "example-worker"), sentPool(fallbackPool, "other.example.org", 3333, "example-worker")),
		args:    []string{"pool", "--url", "pool.example.org", "--user", "example-worker", "--fallback-url", "other.example.org", "--show-user"},
	},
}

func TestPoolWithoutConfirmSendsNoWrite(t *testing.T) {
	for _, tc := range poolCases {
		t.Run(tc.name, func(t *testing.T) {
			host, calls := poolMiner(t, nil, settingsSaved)
			code, out := execute(t, New(func(string) string { return host }), tc.args...)
			want := "host: \"" + host + "\"\n" + tc.body + "sent: false\n" + tc.rows + tc.private +
				"effect: " + poolEffects + "\n" +
				"execute: \"axeos-axi pool --host '" + host + "' " + tc.command + " --confirm\"\n"
			if tc.hiddenUser {
				want += "help[1]: \"a confirmed change of a pool user needs --show-user, so the execute command has it; replace <user> with the new user\"\n"
			}
			if code != 0 || out != want {
				t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
			}
			if calls() != poolRead {
				t.Fatalf("requests=%s", calls())
			}
		})
	}
}

func TestPoolWithConfirmSendsOneWriteWithTheCompleteRecord(t *testing.T) {
	for _, tc := range poolCases {
		if tc.hiddenUser {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			host, calls := poolMiner(t, nil, settingsSaved)
			code, out := execute(t, New(func(string) string { return host }), append(slices.Clone(tc.args), "--confirm")...)
			want := "host: \"" + host + "\"\n" + tc.body + "sent: true\n" + tc.rows + tc.private +
				"result: the miner accepted the request; " + poolEffects + "\n" +
				"note: " + staleReadNote + "\n" +
				"help[3]: \"axeos-axi info --host '" + host + "' --fields stratumURL,stratumPort,fallbackStratumURL,fallbackStratumPort shows the URL and port of each pool that the miner reports; stratumUser and fallbackStratumUser show the users\"," +
				"\"axeos-axi restart --host '" + host + "' for a preview of the restart that makes the miner use the stored values\"," +
				"\"axeos-axi pool --host '" + host + "' " + tc.revert + " --confirm sets the previous values again\"\n"
			if code != 0 || out != want {
				t.Fatalf("code=%d\n%s\nwant\n%s", code, out, want)
			}
			if calls() != poolRead+","+tc.write {
				t.Fatalf("requests=%s", calls())
			}
		})
	}
}

func TestPoolPrintsNoUserWithoutShowUser(t *testing.T) {
	for _, confirm := range []bool{false, true} {
		host, _ := poolMiner(t, nil, settingsSaved)
		args := []string{"pool", "--user", "other-worker", "--fallback-url", "other.example.org"}
		if confirm {
			args = []string{"pool", "--fallback-url", "other.example.org", "--confirm"}
		}
		code, out := execute(t, New(func(string) string { return host }), args...)
		if code != 0 || strings.Contains(out, "example-worker") || strings.Contains(out, "other-worker") {
			t.Fatalf("confirm=%v code=%d\n%s", confirm, code, out)
		}
	}
}

func TestPoolSendsBackEachUnnamedFieldOfTheRecord(t *testing.T) {
	host, calls := poolMiner(t, func(info map[string]any, pools []map[string]any) {
		info["primaryPoolIndex"], info["secondaryPoolIndex"] = float64(1), float64(0)
		pools[1]["stratumProtocol"] = "SV2"
		pools[1]["stratumSuggestedDifficulty"] = float64(512)
		pools[1]["stratumExtranonceSubscribe"] = true
		pools[1]["stratumTLS"] = float64(2)
		pools[1]["stratumCert"] = "example-certificate"
		pools[1]["stratumDecodeCoinbase"] = false
		pools[1]["stratumV2ChannelType"] = "standard"
		pools[1]["stratumV2AuthorityPubkey"] = "example-key"
		pools[1]["stratumV2RequireAuth"] = true
		pools[1]["futureField"] = "kept"
		delete(pools[1], "stratumPassword")
	}, settingsSaved)
	code, out := execute(t, New(func(string) string { return host }), "pool", "--port", "4444", "--confirm")
	if code != 0 || !strings.Contains(out, "sent: true\nsettings[1]{setting,present,new,changes}:\n  port,3333,4444,true\n") || strings.Contains(out, "example-certificate") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	want := poolRead + `,PATCH /api/system {"pools":[{"futureField":"kept","id":1,"stratumCert":"example-certificate","stratumDecodeCoinbase":false,"stratumExtranonceSubscribe":true,"stratumPassword":"*****","stratumPort":4444,` +
		`"stratumProtocol":"SV2","stratumSuggestedDifficulty":512,"stratumTLS":2,"stratumURL":"fallback.example.org","stratumUser":"example-worker","stratumV2AuthorityPubkey":"example-key","stratumV2ChannelType":"standard","stratumV2RequireAuth":true}]}`
	if calls() != want {
		t.Fatalf("requests=%s\nwant\n%s", calls(), want)
	}
}

func TestPoolWithNoChangeSendsOneWriteWithEachNamedPool(t *testing.T) {
	for _, tc := range []struct {
		name, rows, write string
		args              []string
	}{
		{"one value", "settings[1]{setting,present,new,changes}:\n  fallback_port,3333,3333,false\n", sentPools(sentPool(fallbackPool, "fallback.example.org", 3333, "example-worker")), []string{"pool", "--fallback-port", "3333"}},
		{"each value of each pool", "settings[6]{setting,present,new,changes}:\n  url,pool.example.org,pool.example.org,false\n  port,3333,3333,false\n  user,example-worker,example-worker,false\n  fallback_url,fallback.example.org,fallback.example.org,false\n  fallback_port,3333,3333,false\n  fallback_user,example-worker,example-worker,false\n",
			sentPools(sentPool(primaryPool, "pool.example.org", 3333, "example-worker"), sentPool(fallbackPool, "fallback.example.org", 3333, "example-worker")),
			[]string{"pool", "--show-user", "--url", "pool.example.org", "--port", "3333", "--user", "example-worker", "--fallback-url", "fallback.example.org", "--fallback-port", "3333", "--fallback-user", "example-worker"}},
	} {
		for _, confirm := range []bool{false, true} {
			t.Run(tc.name, func(t *testing.T) {
				host, calls := poolMiner(t, nil, settingsSaved)
				args, sent, requests := tc.args, "sent: false\n", poolRead
				if confirm {
					args, sent, requests = append(slices.Clone(args), "--confirm"), "sent: true\n", poolRead+","+tc.write
				}
				code, out := execute(t, New(func(string) string { return host }), args...)
				if code != 0 || !strings.Contains(out, "request: PATCH /api/system\n") || !strings.Contains(out, sent+tc.rows) {
					t.Fatalf("args=%v code=%d\n%s", args, code, out)
				}
				if calls() != requests {
					t.Fatalf("requests=%s", calls())
				}
			})
		}
	}
}

func TestPoolConfirmedUserChangeWithoutShowUserSendsNothing(t *testing.T) {
	host, calls := poolMiner(t, nil, settingsSaved)
	a := New(func(string) string { return host })
	for flag, args := range map[string][]string{
		"--user":          {"pool", "--user", "other-worker", "--confirm"},
		"--fallback-user": {"--confirm", "pool", "--url", "new.example.org", "--fallback-user=other-worker"},
	} {
		code, out := execute(t, a, args...)
		if code != 2 || !strings.Contains(out, "code: usage") || !strings.Contains(out, flag+" with --confirm requires --show-user") || strings.Contains(out, "other-worker") || strings.Contains(out, "example-worker") {
			t.Errorf("args=%v code=%d out=%s", args, code, out)
		}
	}
	if calls() != "" {
		t.Fatalf("requests=%s", calls())
	}
}

func TestPoolPrintedCommandsAreAcceptedForAValueThatStartsWithADash(t *testing.T) {
	host, calls := poolMiner(t, func(_ map[string]any, pools []map[string]any) { pools[0]["stratumUser"] = "-worker" }, settingsSaved)
	a := New(func(string) string { return host })
	code, out := execute(t, a, "pool", "--user=-other", "--show-user")
	if code != 0 || !strings.Contains(out, "execute: \"axeos-axi pool --host '"+host+"' --user='-other' --show-user --confirm\"\n") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	code, out = execute(t, a, "pool", "--user=-other", "--show-user", "--confirm")
	if code != 0 || !strings.Contains(out, "\"axeos-axi pool --host '"+host+"' --user='-worker' --show-user --confirm sets the previous values again\"") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	code, out = execute(t, a, "pool", "--user=-worker", "--show-user", "--confirm")
	if code != 0 || !strings.Contains(out, "sent: true\n") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	write := sentPools(sentPool(primaryPool, "pool.example.org", 3333, "-other"))
	if calls() != poolRead+","+poolRead+","+write+","+poolRead+","+sentPools(sentPool(primaryPool, "pool.example.org", 3333, "-worker")) {
		t.Fatalf("requests=%s", calls())
	}
}

func TestPoolRefusalsSendNoWrite(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		change     func(info map[string]any, pools []map[string]any)
		args       []string
	}{
		{"firmware without the pools list", "code: not_supported\n  message: the miner reports no pools list with a primary and a fallback index; a pool change is not supported for this firmware\n", func(info map[string]any, _ []map[string]any) { delete(info, "pools") }, []string{"--url", "new.example.org"}},
		{"pools that are not a list", "code: not_supported\n", func(info map[string]any, _ []map[string]any) { info["pools"] = "none" }, []string{"--url", "new.example.org"}},
		{"no primary index", "code: not_supported\n", func(info map[string]any, _ []map[string]any) { delete(info, "primaryPoolIndex") }, []string{"--fallback-url", "new.example.org"}},
		{"no fallback index", "code: not_supported\n", func(info map[string]any, _ []map[string]any) { info["secondaryPoolIndex"] = "1" }, []string{"--url", "new.example.org"}},
		{"primary and fallback in one slot", "code: same_slot\n  message: \"the primary and the fallback pool are the same slot 0, so a change of one changes the other; the write is refused\"\n", func(info map[string]any, _ []map[string]any) { info["secondaryPoolIndex"] = float64(0) }, []string{"--url", "new.example.org"}},
		{"no pool in the primary slot", "code: no_pool_in_slot\n  message: \"the miner reports no pool in slot 5, the primary pool; this tool cannot remove a pool again, so the write is refused\"\n", func(info map[string]any, _ []map[string]any) { info["primaryPoolIndex"] = float64(5) }, []string{"--url", "new.example.org"}},
		{"no pool in the fallback slot", "code: no_pool_in_slot\n  message: \"the miner reports no pool in slot 1, the fallback pool; ", func(info map[string]any, pools []map[string]any) { info["pools"] = []any{pools[0]} }, []string{"--url", "new.example.org", "--fallback-port", "4444"}},
		{"no present user", "code: present_value_unknown\n  message: the miner reports no present user for the fallback pool; the write is refused\n", func(_ map[string]any, pools []map[string]any) { delete(pools[1], "stratumUser") }, []string{"--fallback-url", "new.example.org"}},
		{"present port that is not a number", "code: present_value_unknown\n  message: the miner reports no present port for the primary pool; the write is refused\n", func(_ map[string]any, pools []map[string]any) { pools[0]["stratumPort"] = "3333" }, []string{"--port", "4444"}},
		{"present URL with a scheme", "code: not_reversible\n  message: \"the present URL of the primary pool is not a host name or an address of 1 to 255 bytes, without a scheme, a port or a space, so this tool cannot set it again; the write is refused\"\n", func(_ map[string]any, pools []map[string]any) {
			pools[0]["stratumURL"] = "stratum+tcp://pool.example.org"
		}, []string{"--url", "new.example.org"}},
		{"present URL in brackets with a port", "code: not_reversible\n", func(_ map[string]any, pools []map[string]any) { pools[0]["stratumURL"] = "[2001:db8::10]:3333" }, []string{"--url", "new.example.org"}},
		{"present port outside the range", "code: not_reversible\n  message: \"the present port of the fallback pool is not a whole number from 1 to 65535, so this tool cannot set it again; the write is refused\"\n", func(_ map[string]any, pools []map[string]any) { pools[1]["stratumPort"] = float64(0) }, []string{"--fallback-port", "4444"}},
		{"present user that is empty", "code: not_reversible\n  message: \"the present user of the primary pool is not a value of 1 to 255 bytes, so this tool cannot set it again; the write is refused\"\n", func(_ map[string]any, pools []map[string]any) { pools[0]["stratumUser"] = "" }, []string{"--user", "other-worker", "--show-user"}},
	} {
		for _, confirm := range []bool{false, true} {
			t.Run(tc.name, func(t *testing.T) {
				host, calls := poolMiner(t, tc.change, settingsSaved)
				args := append([]string{"pool"}, tc.args...)
				if confirm {
					args = append(args, "--confirm")
				}
				code, out := execute(t, New(func(string) string { return host }), args...)
				if code != 1 || !strings.HasPrefix(out, "error:\n  "+tc.want) || !strings.Contains(out, "\nhelp: \"axeos-axi info --host '"+host+"' ") || strings.Contains(out, "example-worker") {
					t.Fatalf("args=%v code=%d\n%s", args, code, out)
				}
				if calls() != poolRead {
					t.Fatalf("requests=%s", calls())
				}
			})
		}
	}
}

func TestPoolSendsBackAnUnnamedSettingOutsideItsRuleAsRead(t *testing.T) {
	host, calls := poolMiner(t, func(_ map[string]any, pools []map[string]any) {
		pools[0]["stratumURL"] = "stratum+tcp://pool.example.org"
		pools[0]["stratumUser"] = ""
	}, settingsSaved)
	code, out := execute(t, New(func(string) string { return host }), "pool", "--port", "4444", "--confirm")
	if code != 0 || !strings.Contains(out, "sent: true\n") || !strings.Contains(out, "--port=3333 --confirm sets the previous values again") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if calls() != poolRead+","+sentPools(sentPool(primaryPool, "stratum+tcp://pool.example.org", 4444, "")) {
		t.Fatalf("requests=%s", calls())
	}
}

func TestPoolLeavesAnUnnamedPoolUnchecked(t *testing.T) {
	host, calls := poolMiner(t, func(info map[string]any, pools []map[string]any) {
		info["pools"] = []any{pools[0]}
	}, settingsSaved)
	code, out := execute(t, New(func(string) string { return host }), "pool", "--port", "4444", "--confirm")
	if code != 0 || !strings.Contains(out, "sent: true\n") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if calls() != poolRead+","+sentPools(sentPool(primaryPool, "pool.example.org", 4444, "example-worker")) {
		t.Fatalf("requests=%s", calls())
	}
}

func TestPoolReadFailureSendsNoWrite(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer s.Close()
	code, out := execute(t, New(func(string) string { return s.URL }), "pool", "--url", "new.example.org", "--confirm")
	if code != 1 || !strings.HasPrefix(out, "error:\n  code: miner_read_failed\n  message: miner returned HTTP 500 for info\n") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(requests, ",") != poolRead {
		t.Fatalf("requests=%v", requests)
	}
}

func TestPoolWriteFailuresStateWhetherTheRequestWasSent(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		answer     http.HandlerFunc
	}{
		{"rejected by the firmware", "code: pool_failed\n  message: the request was sent; miner returned HTTP 400 for settings; the miner did not confirm the change\n", restartAnswer(http.StatusBadRequest, "text/plain", "Wrong API input")},
		{"outside the allowed network", "code: pool_failed\n  message: the request was sent; miner returned HTTP 401 for settings; the miner did not confirm the change\n", restartAnswer(http.StatusUnauthorized, "text/plain", "private-identifier")},
		{"redirect", "code: pool_failed\n  message: the request was sent; miner returned HTTP 307 for settings; the miner did not confirm the change\n", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/api/system/info", http.StatusTemporaryRedirect)
		}},
		{"closed connection", "code: pool_unconfirmed\n  message: \"the request was sent, but the miner closed the connection or did not answer in time; the change is unconfirmed\"\n", func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host, calls := poolMiner(t, nil, tc.answer)
			code, out := execute(t, New(func(string) string { return host }), "pool", "--url", "new.example.org", "--confirm")
			if code != 1 || !strings.HasPrefix(out, "error:\n  "+tc.want+"help: \"axeos-axi info --host '"+host+"' --fields stratumURL,stratumPort,fallbackStratumURL,fallbackStratumPort shows") || strings.Contains(out, "private-identifier") || strings.Contains(out, "example-worker") {
				t.Fatalf("code=%d\n%s", code, out)
			}
			if !strings.Contains(out, "; axeos-axi pool --host '"+host+"' --url='pool.example.org' --confirm sets the previous values again\"\n") {
				t.Fatalf("no command for the previous values\n%s", out)
			}
			if !strings.Contains(out, "; firmware v2.15.3 reports the pool values from before a write on the next read; a pool call that uses that read sends the old complete record and sets the change back; axeos-axi pool") || strings.Contains(out, "read them before another") {
				t.Fatalf("no stale-read statement\n%s", out)
			}
			if calls() != poolRead+","+sentPools(sentPool(primaryPool, "new.example.org", 3333, "example-worker")) {
				t.Fatalf("requests=%s", calls())
			}
			code, out = execute(t, New(func(string) string { return host }), "pool", "--fallback-user", "other-worker", "--show-user", "--confirm")
			if code != 1 || !strings.Contains(out, "; axeos-axi pool --host '"+host+"' --fallback-user='example-worker' --show-user --confirm sets the previous values again\"\n") {
				t.Fatalf("code=%d\n%s", code, out)
			}
		})
	}
}

func TestPoolRejectsInputBeforeNetwork(t *testing.T) {
	host, calls := poolMiner(t, nil, settingsSaved)
	a := New(func(string) string { return host })
	long := strings.Repeat("a", 256)
	for _, args := range [][]string{
		{"pool"}, {"pool", "--confirm"}, {"pool", "--show-user"}, {"--show-user", "--confirm", "pool"},
		{"pool", "--url"}, {"pool", "--url", "--confirm"}, {"pool", "--url="}, {"pool", "--url", " "},
		{"pool", "--url", "stratum+tcp://pool.example.org"}, {"pool", "--url", "stratum+tcp://pool.example.org:3333"}, {"pool", "--url", "pool.example.org:3333"}, {"pool", "--url", "[2001:db8::10]:3333"}, {"pool", "--url", "[2001:db8::10]"}, {"pool", "--url", "pool example.org"}, {"pool", "--url", long},
		{"pool", "--fallback-url", "stratum+ssl://pool.example.org"}, {"pool", "--fallback-url", "pool.example.org:3333"},
		{"pool", "--port", "0"}, {"pool", "--port", "65536"}, {"pool", "--port", "33.3"}, {"pool", "--port", "stratum"}, {"pool", "--port=-1"}, {"pool", "--fallback-port", "70000"},
		{"pool", "--user="}, {"pool", "--user", long}, {"pool", "--fallback-user", long},
		{"pool", "--url", "a.example.org", "--url", "b.example.org"}, {"pool", "--user=private-identifier", "--user=private-identifier", "--confirm"},
		{"pool", "--fallback-port", "3333", "--fallback-port", "4444", "--confirm"},
		{"pool", "--url", "a.example.org", "--fields", "host"}, {"pool", "--url", "a.example.org", "--lines", "1"}, {"pool", "--url", "a.example.org", "--timeout", "1"},
		{"pool", "--url", "a.example.org", "--password", "x"}, {"pool", "--url", "a.example.org", "--fallback-password", "x"}, {"pool", "--url", "a.example.org", "--tls", "1"},
		{"pool", "--url", "a.example.org", "--frequency", "550"}, {"pool", "--url", "a.example.org", "--confirm=true"}, {"pool", "--url", "a.example.org", "--show-user=true"},
		{"pool", "--url", "a.example.org", "b.example.org"}, {"pool", "--url", "a.example.org", "--confirm", "--help", "--bad"},
		{"--url", "a.example.org"}, {"info", "--url", "a.example.org"}, {"tuning", "--frequency", "550", "--port", "3333"}, {"restart", "--fallback-user", "x", "--confirm"},
		{"info", "--show-user"}, {"restart", "--show-user", "--confirm"}, {"tuning", "--frequency", "550", "--show-user"},
	} {
		code, out := execute(t, a, args...)
		if code != 2 || !strings.Contains(out, "code: usage") || !strings.Contains(out, "valid flags:") || strings.Contains(out, "private-identifier") {
			t.Errorf("args=%v code=%d out=%s", args, code, out)
		}
	}
	for want, args := range map[string][]string{
		"pool requires one or more of --url, --port, --user, --fallback-url, --fallback-port and --fallback-user": {"pool", "--show-user", "--confirm"},
		"--url was given more than once": {"pool", "--url", "a.example.org", "--url", "b.example.org"},
		"--url requires a host name or an address of 1 to 255 bytes, without a scheme, a port or a space":                          {"pool", "--url", "pool.example.org:3333"},
		"--fallback-port requires a whole number from 1 to 65535":                                                                  {"pool", "--fallback-port", "65536"},
		"--user requires a value of 1 to 255 bytes":                                                                                {"pool", "--user", long},
		"unknown flag --fields for `pool`; it prints a fixed result":                                                               {"pool", "--url", "a.example.org", "--fields", "host"},
		"unknown flag --fallback-url; it is a flag of `pool` only":                                                                 {"restart", "--fallback-url", "a.example.org"},
		"unknown flag --show-user; it is a flag of `pool` only":                                                                    {"info", "--show-user"},
		"unknown flag --confirm; it is a flag of `restart`, `tuning` and `pool` only":                                              {"info", "--confirm"},
		"unknown flag --password":                                                                                                  {"pool", "--url", "a.example.org", "--password", "x"},
		"valid flags: --host, --url, --port, --user, --fallback-url, --fallback-port, --fallback-user, --show-user, --confirm, --": {"pool"},
	} {
		if code, out := execute(t, a, args...); code != 2 || !strings.Contains(out, want) {
			t.Errorf("args=%v code=%d out=%s", args, code, out)
		}
	}
	code, out := execute(t, New(func(string) string { return "" }), "pool", "--url", "a.example.org", "--confirm")
	if code != 2 || !strings.Contains(out, "code: host_required") {
		t.Fatalf("%d %s", code, out)
	}
	code, out = execute(t, a, "pool", "--url", "a.example.org", "--confirm", "--host", "http://192.0.2.10/path")
	if code != 2 || !strings.Contains(out, "code: invalid_host") {
		t.Fatalf("%d %s", code, out)
	}
	if calls() != "" {
		t.Fatalf("requests before usage validation: %s", calls())
	}
}

func TestPoolAcceptsTheLimitsOfEachValue(t *testing.T) {
	host, calls := poolMiner(t, nil, settingsSaved)
	longest := strings.Repeat("a", 255)
	code, out := execute(t, New(func(string) string { return host }), "pool", "--url", "2001:db8::10", "--port", "65535", "--user="+longest, "--fallback-url", longest, "--fallback-port", "1", "--show-user", "--confirm")
	if code != 0 || !strings.Contains(out, "sent: true\n") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if calls() != poolRead+","+sentPools(sentPool(primaryPool, "2001:db8::10", 65535, longest), sentPool(fallbackPool, longest, 1, "example-worker")) {
		t.Fatalf("requests=%s", calls())
	}
}

func TestPoolUnreachableMiner(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	host := s.URL
	s.Close()
	code, out := execute(t, New(func(string) string { return host }), "pool", "--url", "new.example.org", "--confirm")
	if code != 1 || !strings.HasPrefix(out, "error:\n  code: miner_read_failed\n") || strings.Contains(out, host) {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestPoolHelpAndVersionSendNothing(t *testing.T) {
	a := New(func(string) string { t.Fatal("offline command read environment"); return "" })
	a.Version = "1.2.3"
	for _, args := range [][]string{{"pool", "--help"}, {"pool", "--user", "private-identifier", "--confirm", "--help"}, {"--help", "pool"}} {
		code, out := execute(t, a, args...)
		if code != 0 || !strings.HasPrefix(out, "command: pool\ndescription: \"Changes the miner: ") || !strings.Contains(out, "url: \"--url <host>; ") || !strings.Contains(out, "fallback_user: \"--fallback-user <user>; ") ||
			!strings.Contains(out, "show_user: \"--show-user; ") || !strings.Contains(out, "examples[4]:") || strings.Contains(out, "--fields") || strings.Contains(out, "private-identifier") {
			t.Fatalf("args=%v code=%d\n%s", args, code, out)
		}
	}
	if code, out := execute(t, a, "pool", "--version"); code != 0 || out != "1.2.3\n" {
		t.Fatalf("%d %s", code, out)
	}
}

func TestHomeNamesPool(t *testing.T) {
	host, calls := miner(t, fixture(t, "info"))
	code, out := execute(t, New(func(string) string { return host }))
	if code != 0 || !strings.Contains(out, "\"axeos-axi pool --host '"+host+"' --url <host> --port <port> --user <user> for a preview of a pool change; it sends no write request without --confirm\"") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if !slices.Equal(calls(), []string{"GET /api/system/info"}) {
		t.Fatalf("requests=%v", calls())
	}
}
