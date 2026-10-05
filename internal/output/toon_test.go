package output

import (
	"bytes"
	"errors"
	"testing"
)

func TestTOONQuoting(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"hello world", "hello world"}, {"true", `"true"`}, {"05", `"05"`}, {"1E+2", `"1E+2"`},
		{"#data", `"#data"`}, {"-data", `"-data"`}, {"a:b", `"a:b"`}, {"a,b", `"a,b"`},
		{"a[b]", `"a[b]"`}, {" leading", `" leading"`}, {"", `""`},
		{"a\nerror: fake", `"a\nerror: fake"`}, {"\b\f", `"\u0008\u000c"`}, {`\backslash`, `"\\backslash"`},
		{"🐟", "🐟"},
	} {
		if got := scalar(tc.input); got != tc.want {
			t.Errorf("%q got %q want %q", tc.input, got, tc.want)
		}
	}
}

func TestTOONShapes(t *testing.T) {
	var b bytes.Buffer
	err := Write(&b, Object{
		{"empty", []any{}}, {"numbers", []any{float64(1), float64(0.000001), float64(10000000)}},
		{"rows", []any{map[string]any{"id": float64(1), "state": "ok"}, map[string]any{"id": float64(2), "state": "a:b"}}},
		{"nested", []any{map[string]any{"meta": map[string]any{"name": "one"}}, map[string]any{"meta": map[string]any{"name": "two"}}}},
		{"matrices", []any{[]any{float64(1), float64(2)}, []any{}}},
		{"objects", []any{map[string]any{"a": "x", "b": []any{float64(1)}}, map[string]any{}}},
		{"unsafe:key", "value"},
	})
	want := "empty: []\nnumbers[3]: 1,0.000001,10000000\nrows[2]{id,state}:\n  1,ok\n  2,\"a:b\"\nnested[2]{meta{name}}:\n  one\n  two\nmatrices[2]:\n  - [2]: 1,2\n  - [0]:\nobjects[2]:\n  - a: x\n    b[1]: 1\n  -\n\"unsafe:key\": value\n"
	if err != nil || b.String() != want {
		t.Fatalf("err=%v\ngot:\n%s\nwant:\n%s", err, b.String(), want)
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestOutputFailure(t *testing.T) {
	if err := Write(brokenWriter{}, Object{{"state", "ok"}}); err == nil {
		t.Fatal("writer failure discarded")
	}
}

func TestKeyedTables(t *testing.T) {
	var b bytes.Buffer
	rows := Object{{"first", map[string]any{"count": float64(1)}}, {"second", map[string]any{"count": float64(2)}}}
	if err := Write(&b, rows); err != nil {
		t.Fatal(err)
	}
	if b.String() != "[2:]{count}:\n  first: 1\n  second: 2\n" {
		t.Fatal(b.String())
	}
	b.Reset()
	if err := Write(&b, Object{{"groups", rows}}); err != nil {
		t.Fatal(err)
	}
	if b.String() != "groups[2:]{count}:\n  first: 1\n  second: 2\n" {
		t.Fatal(b.String())
	}
}
