package app

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

type member struct {
	key   string
	value any
}

type members []member

var toonNumber = regexp.MustCompile(`(?i)^[+-]?[0-9]+(?:\.[0-9]+)?(?:e[+-]?[0-9]+)?$`)

func toonScalar(t *testing.T, s string) any {
	t.Helper()
	switch {
	case strings.HasPrefix(s, `"`):
		var v string
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatalf("quoted value %s: %v", s, err)
		}
		return v
	case s == "true":
		return true
	case s == "false":
		return false
	case s == "null":
		return nil
	case toonNumber.MatchString(s):
		var v float64
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatalf("number %s: %v", s, err)
		}
		return v
	}
	return s
}

func splitCells(s string) []string {
	var cells []string
	var b strings.Builder
	quoted, escaped := false, false
	for _, r := range s {
		switch {
		case escaped:
			escaped = false
		case quoted && r == '\\':
			escaped = true
		case r == '"':
			quoted = !quoted
		case r == ',' && !quoted:
			cells = append(cells, b.String())
			b.Reset()
			continue
		}
		b.WriteRune(r)
	}
	return append(cells, b.String())
}

type toonReader struct {
	t     *testing.T
	lines []string
	at    int
}

func indentOf(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }

func (r *toonReader) key(text string) (string, string) {
	if strings.HasPrefix(text, `"`) {
		end := 1
		for text[end] != '"' {
			if text[end] == '\\' {
				end++
			}
			end++
		}
		return toonScalar(r.t, text[:end+1]).(string), text[end+1:]
	}
	end := strings.IndexAny(text, ":[")
	return text[:end], text[end:]
}

func (r *toonReader) object(indent int) members {
	var out members
	for r.at < len(r.lines) && indentOf(r.lines[r.at]) == indent {
		text := r.lines[r.at][indent:]
		r.at++
		name, rest := r.key(text)
		out = append(out, member{name, r.value(indent, rest)})
	}
	return out
}

func (r *toonReader) value(indent int, rest string) any {
	if strings.HasPrefix(rest, ": ") {
		return toonScalar(r.t, rest[2:])
	}
	if rest == ":" {
		if r.at < len(r.lines) && indentOf(r.lines[r.at]) > indent {
			return r.object(indent + 2)
		}
		return members{}
	}
	closing := strings.Index(rest, "]")
	var count int
	if _, err := fmt.Sscanf(rest[1:closing], "%d", &count); err != nil {
		r.t.Fatalf("array header %q", rest)
	}
	tail := rest[closing+1:]
	switch {
	case count == 0:
		return []any{}
	case strings.HasPrefix(tail, "{"):
		names := splitCells(tail[1:strings.Index(tail, "}")])
		for i, n := range names {
			names[i], _ = r.key(n + ":")
		}
		rows := make([]any, 0, count)
		for range count {
			row := members{}
			for i, cell := range splitCells(strings.TrimSpace(r.lines[r.at])) {
				row = append(row, member{names[i], toonScalar(r.t, cell)})
			}
			r.at++
			rows = append(rows, row)
		}
		return rows
	case strings.HasPrefix(tail, ": "):
		items := []any{}
		for _, cell := range splitCells(tail[2:]) {
			items = append(items, toonScalar(r.t, cell))
		}
		return items
	}
	items := []any{}
	for range count {
		line := r.lines[r.at]
		pad := indentOf(line)
		body := strings.TrimPrefix(line[pad:], "-")
		switch {
		case body == "":
			r.at++
			items = append(items, members{})
		case strings.HasPrefix(body, " ") && (strings.Contains(body, ": ") || strings.HasSuffix(body, ":") || strings.Contains(body, "]:")) && !strings.HasPrefix(strings.TrimSpace(body), `"`):
			r.lines[r.at] = strings.Repeat(" ", pad+2) + body[1:]
			items = append(items, r.object(pad+2))
		default:
			r.at++
			items = append(items, toonScalar(r.t, strings.TrimSpace(body)))
		}
	}
	return items
}

func parseTOON(t *testing.T, text string) members {
	t.Helper()
	r := &toonReader{t: t, lines: strings.Split(strings.TrimSuffix(text, "\n"), "\n")}
	if text == "" {
		return members{}
	}
	out := r.object(0)
	if r.at != len(r.lines) {
		t.Fatalf("unparsed TOON from line %d: %q", r.at, r.lines[r.at])
	}
	return out
}

func decodeJSONValue(t *testing.T, dec *json.Decoder) any {
	t.Helper()
	token, err := dec.Token()
	if err != nil {
		t.Fatal(err)
	}
	switch token {
	case json.Delim('{'):
		out := members{}
		for dec.More() {
			name, err := dec.Token()
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, member{name.(string), decodeJSONValue(t, dec)})
		}
		if _, err := dec.Token(); err != nil {
			t.Fatal(err)
		}
		return out
	case json.Delim('['):
		out := []any{}
		for dec.More() {
			out = append(out, decodeJSONValue(t, dec))
		}
		if _, err := dec.Token(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	return token
}

func parseJSONLines(t *testing.T, text string) []members {
	t.Helper()
	if !strings.HasSuffix(text, "\n") {
		t.Fatalf("JSON output does not end with a newline: %q", text)
	}
	var documents []members
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		dec := json.NewDecoder(strings.NewReader(line))
		document, ok := decodeJSONValue(t, dec).(members)
		if !ok || dec.More() {
			t.Fatalf("not one JSON object: %q", line)
		}
		if err := json.Unmarshal([]byte(line), new(map[string]any)); err != nil {
			t.Fatalf("invalid JSON %q: %v", line, err)
		}
		documents = append(documents, document)
	}
	return documents
}

func maskSeconds(m members) members {
	out := make(members, len(m))
	for i, f := range m {
		out[i] = f
		switch f.key {
		case "seconds_followed":
			out[i].value = "S"
		case "bin":
			out[i].value = "BIN"
		}
	}
	return out
}

func TestJSONCarriesTheValuesOfTheTOONOutput(t *testing.T) {
	for _, tc := range parityCases {
		t.Run(tc.name, func(t *testing.T) {
			toonCode, toon := runParity(t, tc)
			jsonCode, text := runParity(t, tc, "--json")
			if toonCode != jsonCode {
				t.Fatalf("exit code %d with --json, %d without", jsonCode, toonCode)
			}
			want := maskSeconds(parseTOON(t, toon))
			var got members
			documents := parseJSONLines(t, text)
			if !strings.HasPrefix(tc.name, "logs_follow") && len(documents) != 1 {
				t.Fatalf("%d JSON objects, want 1: %s", len(documents), text)
			}
			for _, document := range documents {
				got = append(got, maskSeconds(document)...)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("JSON differs from TOON\njson: %s\ntoon: %s", text, toon)
			}
		})
	}
}

func TestJSONPrintsNoPrivateValueThatTOONHides(t *testing.T) {
	private := []string{"example-worker", "example-fallback", "example-wifi", "02:00:00:00:00:10", "example-bitcoin-address", "example-coinbase"}
	for _, tc := range parityCases {
		if strings.Contains(tc.name, "private") || strings.Contains(tc.name, "show_user") || strings.HasPrefix(tc.name, "help_") {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			_, text := runParity(t, tc, "--json")
			for _, value := range private {
				if strings.Contains(text, value) {
					t.Errorf("%s in %s", value, text)
				}
			}
		})
	}
}

func TestJSONFollowIsOneObjectPerLine(t *testing.T) {
	_, text := runParity(t, parityCase{args: []string{"logs", "--follow", "2"}, host: true}, "--json")
	documents := parseJSONLines(t, text)
	if len(documents) != 4 || documents[0][0].key != "follow_limit_s" || documents[1][0].key != "line_1" || documents[2][0].key != "line_2" {
		t.Fatalf("%s", text)
	}
	closing := map[string]bool{}
	for _, f := range documents[3] {
		closing[f.key] = true
	}
	for _, name := range []string{"lines", "seconds_followed", "ended", "help"} {
		if !closing[name] {
			t.Errorf("closing object lacks %s: %s", name, text)
		}
	}
}

func TestJSONUsageErrorHonorsTheFlagBeforeTheFlagsAreValid(t *testing.T) {
	for _, args := range [][]string{
		{"--bogus", "--json"}, {"--json", "bogus"}, {"info", "--fields", "--json"}, {"info", "--json=1"}, {"--json", "info", "--lines", "3"},
		{"logs", "--json", "--follow", "2", "--lines", "3"}, {"pool", "--json"}, {"skill", "--json"},
	} {
		a := New(func(string) string { return "" })
		code, out := execute(t, a, args...)
		if code != 2 || !strings.HasPrefix(out, `{"error":{"code":"usage","message":`) || strings.Count(out, "\n") != 1 {
			t.Errorf("%v: code=%d out=%s", args, code, out)
		}
	}
}

func TestJSONWithFieldsKeepsTheSelection(t *testing.T) {
	_, text := runParity(t, parityCase{args: []string{"info", "--fields", "version,uptimeSeconds"}, host: true}, "--json")
	documents := parseJSONLines(t, text)
	if len(documents) != 1 || len(documents[0]) != 2 || documents[0][0].key != "version" || documents[0][1].key != "uptimeSeconds" {
		t.Fatalf("%s", text)
	}
}
