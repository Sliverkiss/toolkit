package fc

import (
	"regexp"
	"strings"
)

// ccFeatures is the pre-check set: ordinary requests match none (zero cost).
var ccFeatures = []string{
	"x-anthropic-billing-header",
	"cc_entrypoint=",
	"You are Claude Code",
	"Main branch (",
}

var (
	billingHdrRe = regexp.MustCompile(`(?i)x-anthropic-billing-header:[^;\n]*;?\s*`)
	billingKVRe  = regexp.MustCompile(`(?i)\bcc_[a-z0-9_]+=[^;\n]*;?\s*`)
)

var templateRewrites = [][2]string{
	{
		"You are Claude Code, Anthropic's official CLI for Claude.",
		"You are Claude Code, Anthropic's official CLI tool for Claude.",
	},
	{
		"Main branch (you will usually use this for PRs)",
		"Default branch (you will usually use this for PRs)",
	},
}

// sanitizeText strips CodeBuddy blacklist fingerprints; unchanged input is
// returned as-is. Port of _sanitize_text.
func sanitizeText(text string) string {
	hit := false
	for _, feature := range ccFeatures {
		if strings.Contains(text, feature) {
			hit = true
			break
		}
	}
	if !hit {
		return text
	}
	for _, pair := range templateRewrites {
		text = strings.ReplaceAll(text, pair[0], pair[1])
	}
	if billingHdrRe.MatchString(text) {
		text = billingHdrRe.ReplaceAllString(text, "")
		prev := ""
		for prev != text {
			prev = text
			text = billingKVRe.ReplaceAllString(text, "")
		}
	}
	return strings.TrimSpace(text)
}

// sanitizeContent applies sanitization to a string or the text parts of a
// multimodal array. Port of _sanitize_content.
func sanitizeContent(content any) any {
	switch v := content.(type) {
	case string:
		return sanitizeText(v)
	case []any:
		changed := false
		parts := make([]any, 0, len(v))
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if text, ok := m["text"].(string); ok {
					sanitized := sanitizeText(text)
					if sanitized != text {
						copied := make(map[string]any, len(m))
						for key, value := range m {
							copied[key] = value
						}
						copied["text"] = sanitized
						item = copied
						changed = true
					}
				}
			}
			parts = append(parts, item)
		}
		if changed {
			return parts
		}
		return content
	default:
		return content
	}
}

// SanitizeWireBody sanitizes the messages of a wire body. The original body is
// returned unchanged when nothing matched. Port of sanitize_wire_body.
func SanitizeWireBody(body map[string]any) map[string]any {
	rawMessages, ok := body["messages"].([]any)
	if !ok {
		return body
	}
	out := make([]any, 0, len(rawMessages))
	changed := false
	for _, item := range rawMessages {
		msg, ok := item.(map[string]any)
		if !ok {
			out = append(out, item)
			continue
		}
		content := msg["content"]
		sanitized := sanitizeContent(content)
		if sameContent(sanitized, content) {
			out = append(out, msg)
			continue
		}
		changed = true
		copied := make(map[string]any, len(msg))
		for key, value := range msg {
			copied[key] = value
		}
		copied["content"] = sanitized
		out = append(out, copied)
	}
	if !changed {
		return body
	}
	result := make(map[string]any, len(body))
	for key, value := range body {
		result[key] = value
	}
	result["messages"] = out
	return result
}

// sameContent reports identity for strings/arrays, mirroring Python's `is`.
func sameContent(a, b any) bool {
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case []any:
		if _, ok := b.([]any); !ok {
			return false
		}
		// sanitizeContent only returns a new slice when something changed, so
		// pointer identity of the backing array is the faithful check.
		return len(av) > 0 && len(b.([]any)) > 0 && &av[0] == &b.([]any)[0]
	default:
		return a == b
	}
}
