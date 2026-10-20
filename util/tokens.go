package util

import (
	"fmt"
	"strings"

	"github.com/Sliverkiss/toolkit/jsonx"
	"github.com/Sliverkiss/toolkit/models"
)

// EstimateTokens is a ~4-chars-per-token heuristic. Port of estimate_tokens.
func EstimateTokens(text any) int {
	if text == nil {
		return 0
	}
	s, ok := text.(string)
	if !ok {
		s = fmt.Sprint(text)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	return max(1, (len(s)+3)/4)
}

// MessagesText serializes request messages to one string for prompt-token
// estimation. Port of messages_text.
func MessagesText(req *models.CanonicalRequest) string {
	var parts []string
	for _, msg := range req.Messages {
		if len(msg.Raw) > 0 {
			parts = append(parts, fmt.Sprint(msg.Raw))
			continue
		}
		content := msg.Content
		if list, ok := content.([]any); ok {
			strs := make([]string, 0, len(list))
			for _, c := range list {
				strs = append(strs, fmt.Sprint(c))
			}
			content = strings.Join(strs, " ")
		}
		parts = append(parts, fmt.Sprintf("%s: %v", msg.Role, content))
		for _, tc := range msg.ToolCalls {
			parts = append(parts, fmt.Sprintf("%s: %v", tc.Name, tc.Arguments))
		}
	}
	return strings.Join(parts, "\n")
}

func isZeroUsage(usage map[string]any) bool {
	if len(usage) == 0 {
		return true
	}
	for _, key := range []string{"prompt_tokens", "completion_tokens", "total_tokens", "input_tokens", "output_tokens"} {
		if !jsonx.Falsy(usage[key]) {
			return false
		}
	}
	return true
}

// ResolveUsage returns the best-effort usage: real upstream values if present,
// otherwise a local character-count estimate, always enriched with local cache
// statistics. Port of resolve_usage.
func ResolveUsage(upstream map[string]any, promptText, completionText string) map[string]any {
	if !isZeroUsage(upstream) {
		out := make(map[string]any, len(upstream)+8)
		for k, v := range upstream {
			out[k] = v
		}
		pt := jsonx.IntValue(out["prompt_tokens"])
		if pt == 0 {
			pt = jsonx.IntValue(out["input_tokens"])
		}
		ct := jsonx.IntValue(out["completion_tokens"])
		if ct == 0 {
			ct = jsonx.IntValue(out["output_tokens"])
		}
		out["prompt_tokens"] = pt
		out["completion_tokens"] = ct
		if total := jsonx.IntValue(out["total_tokens"]); total != 0 {
			out["total_tokens"] = total
		} else {
			out["total_tokens"] = pt + ct
		}
		return EnrichUsage(out, promptText, pt)
	}
	pt := EstimateTokens(promptText)
	ct := EstimateTokens(completionText)
	out := map[string]any{
		"prompt_tokens":     pt,
		"completion_tokens": ct,
		"total_tokens":      pt + ct,
	}
	return EnrichUsage(out, promptText, pt)
}
