package jsonx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// TryJSONValue decodes a JSON document, preserving the int/float distinction the
// way Python's json.loads does, and requiring the whole input to be consumed.
func TryJSONValue(text string) (any, bool) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, false
	}
	if strings.TrimSpace(text[dec.InputOffset():]) != "" {
		return nil, false
	}
	return convertNumbers(value), true
}

func convertNumbers(value any) any {
	switch v := value.(type) {
	case json.Number:
		s := v.String()
		if !strings.ContainsAny(s, ".eE") {
			if i, err := v.Int64(); err == nil {
				return i
			}
		}
		if f, err := v.Float64(); err == nil {
			return f
		}
		return s
	case map[string]any:
		for k, child := range v {
			v[k] = convertNumbers(child)
		}
		return v
	case []any:
		for i, child := range v {
			v[i] = convertNumbers(child)
		}
		return v
	default:
		return v
	}
}

// Dumps is compact JSON with HTML escaping disabled — Python's
// json.dumps(value, ensure_ascii=False, separators=(",", ":"), default=str).
// Non-marshalable values are stringified rather than failing.
func Dumps(value any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "")
	if err := enc.Encode(stringifyUnsupported(value)); err != nil {
		return "null"
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

func stringifyUnsupported(value any) any {
	switch v := value.(type) {
	case nil, bool, string, int, int64, float64, json.Number, map[string]any, []any:
		return v
	case []string:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = item
		}
		return out
	case map[string]string:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = item
		}
		return out
	case int32:
		return int64(v)
	case float32:
		return float64(v)
	default:
		return Str(v)
	}
}

// Str coerces a scalar to its Python-str equivalent (empty for nil/other).
func Str(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return FormatFloat(v)
	case float32:
		return FormatFloat(float64(v))
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case bool:
		if v {
			return "True"
		}
		return "False"
	case map[string]any, []any:
		return Dumps(v)
	default:
		return ""
	}
}

// FormatFloat formats a float like Python's repr for integral values.
func FormatFloat(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// AsList normalizes a JSON-ish value to a list (Python _as_list).
func AsList(value any) []any {
	switch v := value.(type) {
	case nil:
		return nil
	case []any:
		return v
	case []map[string]any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, item)
		}
		return out
	case []string:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, item)
		}
		return out
	default:
		return []any{value}
	}
}

// IsMapping reports whether value is a JSON object.
func IsMapping(value any) bool {
	_, ok := value.(map[string]any)
	return ok
}

// IntValue mirrors Python's `int(value or 0)` for JSON scalars.
func IntValue(value any) int { return intValue(value) }

// Falsy mirrors Python truthiness for scalar JSON values.
func Falsy(value any) bool { return isFalsy(value) }

func intValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case bool:
		if v {
			return 1
		}
		return 0
	case string:
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}

// isFalsy mirrors Python truthiness for scalar JSON values.
func isFalsy(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case bool:
		return !v
	case string:
		return v == ""
	case int:
		return v == 0
	case int64:
		return v == 0
	case float64:
		return v == 0
	case map[string]any:
		return len(v) == 0
	case []any:
		return len(v) == 0
	default:
		return false
	}
}
