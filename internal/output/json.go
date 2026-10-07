package output

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
)

type jsonSink struct{ w io.Writer }

// JSON returns a writer on which Write encodes each result as one JSON object on one line instead of TOON.
func JSON(w io.Writer) io.Writer { return jsonSink{w} }

func (s jsonSink) Write(p []byte) (int, error) { return s.w.Write(p) }

func jsonString(b *bytes.Buffer, v any) error {
	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	b.Truncate(b.Len() - 1)
	return nil
}

func jsonValue(b *bytes.Buffer, v any) error {
	if o, ok := object(v); ok {
		return jsonObject(b, o)
	}
	switch v := v.(type) {
	case []any:
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := jsonValue(b, item); err != nil {
				return err
			}
		}
		b.WriteByte(']')
		return nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			b.WriteString("null")
			return nil
		}
	}
	return jsonString(b, v)
}

func jsonObject(b *bytes.Buffer, o Object) error {
	b.WriteByte('{')
	for i, f := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		if err := jsonString(b, f.Name); err != nil {
			return err
		}
		b.WriteByte(':')
		if err := jsonValue(b, f.Value); err != nil {
			return err
		}
	}
	b.WriteByte('}')
	return nil
}

func writeJSON(w io.Writer, fields Object) error {
	var b bytes.Buffer
	if err := jsonObject(&b, fields); err != nil {
		return err
	}
	b.WriteByte('\n')
	_, err := w.Write(b.Bytes())
	return err
}
