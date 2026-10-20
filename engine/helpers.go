package engine

import (
	"regexp"
	"strings"

	"github.com/Sliverkiss/toolkit/jsonx"
)

// tryJSON is the Python _try_json: ok, parsed.
func tryJSON(text any) (bool, any) {
	s, ok := text.(string)
	if !ok {
		if text == nil {
			return false, nil
		}
		s = jsonx.Str(text)
	}
	value, ok2 := jsonx.TryJSONValue(s)
	return ok2, value
}

// jsonDumps is the Python _json_dumps (compact, ensure_ascii=False, default=str).
func jsonDumps(value any) string { return jsonx.Dumps(value) }

// stableStringify is the Python _stable_stringify used by dedupe.
func stableStringify(value any) string {
	switch v := value.(type) {
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = stableStringify(item)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sortStrings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, jsonDumps(key)+":"+stableStringify(v[key]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	default:
		return jsonDumps(value)
	}
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// htmlUnescape is the Python _html_unescape.
func htmlUnescape(value any) string {
	return strings.NewReplacer(
		"&quot;", `"`,
		"&apos;", "'",
		"&lt;", "<",
		"&gt;", ">",
		"&amp;", "&",
	).Replace(jsonx.Str(value))
}

// escapeXML is the Python _escape_xml.
func escapeXML(value any) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	).Replace(jsonx.Str(value))
}

// toolAliasKey is the Python _tool_alias_key.
func toolAliasKey(value any) string {
	return aliasKeyRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(jsonx.Str(value))), "")
}

func firstString(values ...any) string {
	for _, value := range values {
		s, ok := value.(string)
		if ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func firstDefined(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

// clip is the Python _clip.
func clip(text any, maximum int) string {
	value := strings.TrimSpace(jsonx.Str(text))
	if len(value) > maximum {
		return value[:maximum] + "..."
	}
	return value
}

// containsFold reports a case-insensitive substring check.
func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

func countMatches(re *regexp.Regexp, text string) int {
	return len(re.FindAllStringIndex(text, -1))
}

func lastMatchIndex(re *regexp.Regexp, text string) int {
	locs := re.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return -1
	}
	return locs[len(locs)-1][0]
}
