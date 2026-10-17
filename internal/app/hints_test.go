package app

import (
	"regexp"
	"testing"
)

var (
	helpFieldLine = regexp.MustCompile(`(?m)^help(?:\[\d+\])?: (.*)$`)
	backtickSpan  = regexp.MustCompile("`[^`]*`")
	bareCommand   = regexp.MustCompile(`axeos-axi( |$)`)
)

func commandOutsideBackticks(out string) string {
	for _, m := range helpFieldLine.FindAllStringSubmatch(out, -1) {
		if bareCommand.MatchString(backtickSpan.ReplaceAllString(m[1], "")) {
			return m[0]
		}
	}
	return ""
}

func TestHintsNameCommandsInsideBackticks(t *testing.T) {
	if commandOutsideBackticks("help: axeos-axi discover finds miners\n") == "" {
		t.Fatal("a command outside backticks went unnoticed")
	}
	if commandOutsideBackticks("help: Run `axeos-axi discover` to find miners; then axeos-axi host save\n") == "" {
		t.Fatal("a second command outside backticks went unnoticed")
	}
	if line := commandOutsideBackticks("help[2]: Run `axeos-axi --host <address>` for health,\"Run `axeos-axi discover` for detail\"\n"); line != "" {
		t.Fatalf("a quoted command was flagged: %s", line)
	}
	for _, tc := range parityCases {
		t.Run(tc.name, func(t *testing.T) {
			_, out := runParity(t, tc)
			if line := commandOutsideBackticks(out); line != "" {
				t.Fatalf("hint names a command outside backticks:\n%s", line)
			}
		})
	}
}
