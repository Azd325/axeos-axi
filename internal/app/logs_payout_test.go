package app

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Azd325/axeos-axi/internal/ws/wstest"
)

const (
	legacyAddress  = "1Qr7wVxTn4KeZbm8Pd3HfGcYsUa2NjMh5E"
	p2shAddress    = "3Kx9ZpTe2VbWmNd4RqYsHcJg7AfUa8NuMr"
	bech32Address  = "bc1qexample0test0address0for0fixtures00x7z"
	taprootAddress = "bc1pexample0test0taproot0address0for0fixtures0000000000x7z"
	testnetAddress = "tb1qexample0test0address0for0fixtures00x7z"
	templateParams = `["5fa667","a1b2c3","01000000010000","ffffffff2003","2f506f6f6c2f","0600000000000000"]`
)

var payoutCases = []struct{ name, line, want string }{
	{
		"owner output, legacy",
		"I (10) stratum_v1_task:   Output 0: " + legacyAddress + " (314495669 sat) (Your payout address)",
		"I (10) stratum_v1_task:   Output 0: <payout-address> (314495669 sat) (Your payout address)",
	},
	{
		"other output, P2SH",
		"I (11) stratum_v1_task:   Output 1: " + p2shAddress + " (1250 sat)",
		"I (11) stratum_v1_task:   Output 1: <payout-address> (1250 sat)",
	},
	{
		"output, bech32",
		"I (12) stratum_v1_task:   Output 2: " + bech32Address + " (99 sat)",
		"I (12) stratum_v1_task:   Output 2: <payout-address> (99 sat)",
	},
	{
		"output, taproot",
		"I (13) stratum_v2_task:   Output 3: " + taprootAddress + " (7 sat) (Your payout address)",
		"I (13) stratum_v2_task:   Output 3: <payout-address> (7 sat) (Your payout address)",
	},
	{
		"output, testnet",
		"I (14) stratum_v1_task:   Output 0: " + testnetAddress + " (50 sat)",
		"I (14) stratum_v1_task:   Output 0: <payout-address> (50 sat)",
	},
	{
		"output without value",
		"I (15) stratum_v1_task:   Output 4: " + legacyAddress,
		"I (15) stratum_v1_task:   Output 4: <payout-address>",
	},
	{
		"output on master, tag system",
		"I (16) system:   Output 0: " + bech32Address + " (5 sat) (Your payout address)",
		"I (16) system:   Output 0: <payout-address> (5 sat) (Your payout address)",
	},
	{
		"OP_RETURN text",
		"I (17) stratum_v1_task:   Output 5: OP_RETURN: .!..'....9.6..q.d (0 sat)",
		"I (17) stratum_v1_task:   Output 5: <output-script> (0 sat)",
	},
	{
		"OP_RETURN text with spaces and no value",
		"I (18) stratum_v1_task:   Output 6: OP_RETURN: pool tag here",
		"I (18) stratum_v1_task:   Output 6: <output-script>",
	},
	{
		"hex fallback form",
		"I (19) stratum_v1_task:   Output 7: P2WPKH:00112233445566778899aabbccddeeff00112233 (3 sat)",
		"I (19) stratum_v1_task:   Output 7: <output-script> (3 sat)",
	},
	{
		"unknown script",
		"I (20) stratum_v2_task:   Output 8: UNKNOWN:6a0b68656c6c6f (0 sat)",
		"I (20) stratum_v2_task:   Output 8: <output-script> (0 sat)",
	},
	{
		"scriptsig",
		"I (21) stratum_v1_task: Scriptsig: /ExamplePool/mined by example/",
		"I (21) stratum_v1_task: Scriptsig: <scriptsig>",
	},
	{
		"received mining.notify",
		`I (22) stratum_api: rx: {"id":null,"method":"mining.notify","params":` + templateParams + `}`,
		`I (22) stratum_api: rx: {"id":null,"method":"mining.notify","params":<redacted: block template, contains pool tag and payout script>}`,
	},
	{
		"received mining.notify, params first",
		`I (23) stratum_v1: rx: {"params":` + templateParams + `,"id":null,"method":"mining.notify"}`,
		`I (23) stratum_v1: rx: {"params":<redacted: block template, contains pool tag and payout script>,"id":null,"method":"mining.notify"}`,
	},
	{
		"received mining.notify, cut off",
		`I (24) stratum_api: rx: {"id":null,"method":"mining.notify","params":["5fa667","a1b2c3","01000`,
		`I (24) stratum_api: rx: {"id":null,"method":"mining.notify","params":<redacted: block template, contains pool tag and payout script>`,
	},
	{
		"received mining.notify, bracket inside a string",
		`I (25) stratum_api: rx: {"id":null,"method":"mining.notify","params":["a]b\"c","d"]}`,
		`I (25) stratum_api: rx: {"id":null,"method":"mining.notify","params":<redacted: block template, contains pool tag and payout script>}`,
	},
	{
		"sent mining.authorize",
		`I (26) stratum_api: tx: {"id":3,"method":"mining.authorize","params":["` + legacyAddress + `.rig1","x"]}`,
		`I (26) stratum_api: tx: {"id":3,"method":"mining.authorize","params":["<pool-user>","x"]}`,
	},
	{
		"sent mining.submit",
		`I (27) stratum_api: tx: {"id":101,"method":"mining.submit","params":["` + legacyAddress + `","5fa667","06","6ac6970a","89f82f46","000a0000"]}`,
		`I (27) stratum_api: tx: {"id":101,"method":"mining.submit","params":["<pool-user>","5fa667","06","6ac6970a","89f82f46","000a0000"]}`,
	},
	{
		"stratum v2 channel",
		"I (28) stratum_v2_task: Opening extended mining channel (user=" + legacyAddress + ".rig1)",
		"I (28) stratum_v2_task: Opening extended mining channel (user=<pool-user>)",
	},
	{
		"stratum message that failed to parse",
		`E (29) stratum_api: JSON parse failed: {"method":"mining.notify","params":["` + legacyAddress + `"`,
		"E (29) stratum_api: JSON parse failed: <redacted: unparsed stratum message>",
	},
}

var unchangedCases = []string{
	"I (40) stratum_v1_task: Coinbase outputs: 2, total value: 314495669 sats",
	"I (41) create_jobs_task: New Work Dequeued 5fa667",
	"I (42) asic_result: ID: 5fa667, ASIC nr: 0, Core: 35/0, ver: 200A0000 Nonce 89F82F46 diff 5126.7 of 4096.",
	`I (43) stratum_api: rx: {"id":101,"error":null,"result":true}`,
	`I (44) stratum_api: rx: {"id":null,"method":"mining.set_difficulty","params":[4096]}`,
	"I (45) stratum_v1_task: Set pool difficulty: 4096.00",
	"I (46) example: Output 0 of the test passed",
}

func payoutInput() ([]string, []string) {
	var in, want []string
	for _, c := range payoutCases {
		in, want = append(in, c.line), append(want, c.want)
	}
	return append(in, unchangedCases...), append(want, unchangedCases...)
}

func TestLogsReplacePayoutAddressAndBlockTemplate(t *testing.T) {
	in, want := payoutInput()
	host, _ := logsMiner(t, http.StatusOK, "text/plain", strings.Join(in, "\n")+"\n")
	code, out := execute(t, New(func(string) string { return host }), "logs", "--lines", "all")
	if code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	for i, w := range want {
		if !strings.Contains(out, "  \""+strings.ReplaceAll(w, `"`, `\"`)+"\"\n") {
			t.Errorf("case %d: missing %q in\n%s", i, w, out)
		}
	}
	for _, leaked := range []string{legacyAddress, p2shAddress, bech32Address, taprootAddress, testnetAddress, "a1b2c3", "ExamplePool", "OP_RETURN: .", "pool tag here", "00112233"} {
		if strings.Contains(out, leaked) {
			t.Errorf("%s leaked in %s", leaked, out)
		}
	}
}

func TestLogsShowPrivateKeepsPayoutAddressAndBlockTemplate(t *testing.T) {
	in, _ := payoutInput()
	host, _ := logsMiner(t, http.StatusOK, "text/plain", strings.Join(in, "\n")+"\n")
	code, out := execute(t, New(func(string) string { return host }), "logs", "--lines", "all", "--show-private")
	if code != 0 || strings.Contains(out, "<payout-address>") || strings.Contains(out, "<redacted") {
		t.Fatalf("%d %s", code, out)
	}
	for _, kept := range []string{legacyAddress, p2shAddress, bech32Address, taprootAddress, testnetAddress, "a1b2c3", "ExamplePool", "pool tag here"} {
		if !strings.Contains(out, kept) {
			t.Errorf("%s missing in %s", kept, out)
		}
	}
}

func followPayout(t *testing.T, args ...string) string {
	t.Helper()
	shortFollow(t)
	in, _ := payoutInput()
	m := newStreamMiner(t, func(w http.ResponseWriter, r *http.Request) {
		s := wstest.Upgrade(w, r, "")
		defer func() { _ = s.Conn.Close() }()
		for _, line := range in {
			s.Text(line + "\n")
		}
		time.Sleep(time.Second)
	})
	code, out := execute(t, New(func(string) string { return m.url }), append([]string{"logs", "--follow", "5"}, args...)...)
	if code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	return out
}

func TestFollowReplacesPayoutAddressAndBlockTemplate(t *testing.T) {
	out := followPayout(t)
	_, want := payoutInput()
	for i, w := range want {
		field := "line_" + strconv.Itoa(i+1) + ": \"" + strings.ReplaceAll(w, `"`, `\"`) + "\"\n"
		if !strings.Contains(out, field) {
			t.Errorf("case %d: missing %q in\n%s", i, field, out)
		}
	}
	for _, leaked := range []string{legacyAddress, p2shAddress, bech32Address, taprootAddress, testnetAddress, "a1b2c3", "ExamplePool"} {
		if strings.Contains(out, leaked) {
			t.Errorf("%s leaked in %s", leaked, out)
		}
	}
}

func TestFollowShowPrivateKeepsPayoutAddressAndBlockTemplate(t *testing.T) {
	out := followPayout(t, "--show-private")
	if strings.Contains(out, "<payout-address>") || strings.Contains(out, "<redacted") {
		t.Fatal(out)
	}
	for _, kept := range []string{legacyAddress, p2shAddress, bech32Address, taprootAddress, testnetAddress, "a1b2c3", "ExamplePool"} {
		if !strings.Contains(out, kept) {
			t.Errorf("%s missing in %s", kept, out)
		}
	}
}

func TestLogsReplacesCutFirstLineButKeepsRestartMarker(t *testing.T) {
	cut := `"` + bech32Address + `","ExamplePool"],"id":null,"method":"mining.notify"}`
	for _, tc := range []struct{ first, want string }{
		{cut, "<redacted: cut end of a longer line>"},
		{"--- SYSTEM RESTART ---", "--- SYSTEM RESTART ---"},
	} {
		host, _ := logsMiner(t, http.StatusOK, "text/plain", tc.first+"\nI (2) example: next line\n")
		code, out := execute(t, New(func(string) string { return host }), "logs", "--lines", "all")
		if code != 0 || !strings.Contains(out, `"`+tc.want+`"`) || strings.Contains(out, "ExamplePool") || strings.Contains(out, bech32Address) {
			t.Errorf("%q: %d %s", tc.first, code, out)
		}
	}
}

func TestLogsShowPrivateKeepsCutFirstLine(t *testing.T) {
	host, _ := logsMiner(t, http.StatusOK, "text/plain", `"ExamplePool"],"id":null`+"\nI (2) example: next line\n")
	code, out := execute(t, New(func(string) string { return host }), "logs", "--lines", "all", "--show-private")
	if code != 0 || !strings.Contains(out, "ExamplePool") || strings.Contains(out, "<redacted") {
		t.Fatalf("%d %s", code, out)
	}
}

func TestFollowReplacesEveryPieceOfALongLine(t *testing.T) {
	shortFollow(t)
	filler := strings.Repeat("x", 8<<10)
	m := newStreamMiner(t, func(w http.ResponseWriter, r *http.Request) {
		s := wstest.Upgrade(w, r, "")
		defer func() { _ = s.Conn.Close() }()
		s.Text(`I (1) stratum_api: rx: {"params":["a1b2c3","` + filler)
		for range 8 {
			s.Text(filler)
		}
		s.Text(`","ExamplePool","` + bech32Address + `"],"method":"mining.notify"}` + "\nI (2) example: next line\n")
		time.Sleep(time.Second)
	})
	code, out := execute(t, New(func(string) string { return m.url }), "logs", "--follow", "5")
	if code != 0 || strings.Contains(out, "ExamplePool") || strings.Contains(out, bech32Address) || strings.Contains(out, "a1b2c3") || strings.Contains(out, "xxx") || strings.Contains(out, "stratum_api") {
		t.Fatalf("%d leak in %.300s", code, out)
	}
	if strings.Count(out, "<redacted: cut end of a longer line>") != 2 || !strings.Contains(out, "next line") {
		t.Fatalf("%.600s", out)
	}
}

func TestFollowShowPrivateKeepsLongLine(t *testing.T) {
	shortFollow(t)
	m := newStreamMiner(t, func(w http.ResponseWriter, r *http.Request) {
		s := wstest.Upgrade(w, r, "")
		defer func() { _ = s.Conn.Close() }()
		for range 9 {
			s.Text(strings.Repeat("x", 8<<10))
		}
		s.Text("ExamplePool\n")
		time.Sleep(time.Second)
	})
	code, out := execute(t, New(func(string) string { return m.url }), "logs", "--follow", "5", "--show-private")
	if code != 0 || strings.Contains(out, "<redacted") || !strings.Contains(out, "ExamplePool") {
		t.Fatalf("%d %.300s", code, out)
	}
}
