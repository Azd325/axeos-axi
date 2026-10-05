package output

import (
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Field struct {
	Name  string
	Value any
}

type Object []Field

var keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)
var numberPattern = regexp.MustCompile(`(?i)^[+-]?[0-9]+(?:\.[0-9]+)?(?:e[+-]?[0-9]+)?$`)

func quoted(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 32 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func key(s string) string {
	if keyPattern.MatchString(s) {
		return s
	}
	return quoted(s)
}

func scalar(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case string:
		quote := v == "" || strings.TrimSpace(v) != v || v == "true" || v == "false" || v == "null" || numberPattern.MatchString(v) || strings.ContainsAny(v, ":,\"\\[]{}") || strings.HasPrefix(v, "-") || strings.HasPrefix(v, "#")
		for _, r := range v {
			quote = quote || r < 32
		}
		if quote {
			return quoted(v)
		}
		return v
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return "null"
		}
		if v == 0 {
			return "0"
		}
		if math.Abs(v) >= 1e-6 && math.Abs(v) < 1e21 {
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
		return strconv.FormatFloat(v, 'e', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	default:
		return fmt.Sprint(v)
	}
}

// API object keys are sorted; explicitly constructed views retain field order.
func object(v any) (Object, bool) {
	switch v := v.(type) {
	case Object:
		return v, true
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fields := make(Object, 0, len(keys))
		for _, k := range keys {
			fields = append(fields, Field{k, v[k]})
		}
		return fields, true
	}
	return nil, false
}

func primitive(v any) bool {
	_, isObject := object(v)
	_, isArray := v.([]any)
	return !isObject && !isArray
}

func columns(v any) (string, []string, bool) {
	o, ok := object(v)
	if !ok || len(o) == 0 {
		return "", nil, false
	}
	var names, cells []string
	for _, f := range o {
		if !primitive(f.Value) {
			return "", nil, false
		}
		names = append(names, key(f.Name))
		cells = append(cells, scalar(f.Value))
	}
	return strings.Join(names, ","), cells, true
}

func field(b *strings.Builder, name string, v any, depth int) {
	indent := strings.Repeat("  ", depth)
	if o, ok := object(v); ok {
		fmt.Fprintf(b, "%s%s:\n", indent, name)
		for _, f := range o {
			field(b, key(f.Name), f.Value, depth+1)
		}
		return
	}
	if a, ok := v.([]any); ok {
		if len(a) == 0 {
			fmt.Fprintf(b, "%s%s[0]:\n", indent, name)
			return
		}
		allPrimitive := true
		for _, item := range a {
			allPrimitive = allPrimitive && primitive(item)
		}
		if allPrimitive {
			values := make([]string, len(a))
			for i, item := range a {
				values[i] = scalar(item)
			}
			fmt.Fprintf(b, "%s%s[%d]: %s\n", indent, name, len(a), strings.Join(values, ","))
			return
		}
		header, _, table := columns(a[0])
		table = table && name != ""
		rows := make([]string, len(a))
		for i, item := range a {
			h, cells, ok := columns(item)
			table = table && ok && h == header
			rows[i] = strings.Join(cells, ",")
		}
		if table {
			fmt.Fprintf(b, "%s%s[%d]{%s}:\n", indent, name, len(a), header)
			for _, row := range rows {
				fmt.Fprintf(b, "%s  %s\n", indent, row)
			}
			return
		}
		fmt.Fprintf(b, "%s%s[%d]:\n", indent, name, len(a))
		for _, item := range a {
			if primitive(item) {
				fmt.Fprintf(b, "%s  - %s\n", indent, scalar(item))
			} else if o, ok := object(item); ok {
				if len(o) == 0 {
					fmt.Fprintf(b, "%s  -\n", indent)
					continue
				}
				var nested strings.Builder
				for _, f := range o {
					field(&nested, key(f.Name), f.Value, depth+2)
				}
				text := nested.String()
				fmt.Fprintf(b, "%s  - %s", indent, strings.TrimPrefix(text, indent+"    "))
			} else {
				var nested strings.Builder
				field(&nested, "", item, depth+1)
				fmt.Fprintf(b, "%s  - %s", indent, strings.TrimPrefix(nested.String(), indent+"  "))
			}
		}
		return
	}
	fmt.Fprintf(b, "%s%s: %s\n", indent, name, scalar(v))
}

func Write(w io.Writer, fields Object) error {
	var b strings.Builder
	for _, f := range fields {
		field(&b, key(f.Name), f.Value, 0)
	}
	_, err := io.WriteString(w, b.String())
	return err
}
