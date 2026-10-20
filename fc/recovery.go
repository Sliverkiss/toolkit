package fc

import (
	"regexp"
	"strings"

	"github.com/Sliverkiss/toolkit/models"
)

var openMarkerPatterns = []string{
	`<[|:][A-Za-z0-9_]+\s*[|:]\s*tool_calls\b`,
	`<[|:][A-Za-z0-9_]+\s*[|: \t]*invoke\b`,
	`<tool_calls>`,
	`<tool_use>`,
	`"tool_calls"\s*:`,
	`function\.name\s*:`,
}

var closeMarkerPatterns = []string{
	`</[|:][A-Za-z0-9_]+\s*[|:]?\s*(?:invoke|tool_calls|tool_use)\b`,
	`</[|:][A-Za-z0-9_]+\s*[|:]?\s*>`,
	`</tool_calls>`,
	`</tool_use>`,
}

var (
	openMarkerRes  = compileAll(openMarkerPatterns)
	closeMarkerRes = compileAll(closeMarkerPatterns)
)

func compileAll(patterns []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(patterns))
	for i, pattern := range patterns {
		out[i] = regexp.MustCompile("(?i)" + pattern)
	}
	return out
}

func anyMatch(res []*regexp.Regexp, text string) bool {
	for _, re := range res {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

// IsToolCallTruncated reports whether text contains an unclosed tool envelope.
// Port of is_tool_call_truncated.
func IsToolCallTruncated(text string) bool {
	if text == "" {
		return false
	}
	if !anyMatch(openMarkerRes, text) {
		return false
	}
	if anyMatch(closeMarkerRes, text) {
		trimmed := strings.TrimRight(text, " \t\n\r\f\v")
		for _, suffix := range []string{",", ":", "{", "["} {
			if strings.HasSuffix(trimmed, suffix) {
				return true
			}
		}
		return false
	}
	return true
}

// BuildRetryUserMessage builds the retry prompt. Port of
// build_retry_user_message.
func BuildRetryUserMessage(originalOutput, reason string) string {
	snippet := originalOutput
	if len(snippet) > 2000 {
		snippet = snippet[len(snippet)-2000:]
	}
	if reason == "truncated" {
		return "Your previous tool-call output was truncated mid-envelope. " +
			"Re-emit the COMPLETE tool call protocol block only, with no extra commentary.\n\n" +
			"Previous output (truncated):\n" + snippet
	}
	return "Your previous tool-call output could not be parsed. " +
		"Re-emit a valid tool call protocol block for the listed tools only.\n\n" +
		"Previous output:\n" + snippet
}

// ParseWithRecoveryHint parses once and, when empty, reports why a retry is
// warranted. Port of parse_with_recovery_hint.
func ParseWithRecoveryHint(text string, tools []models.ToolDef, protocol string, stripThink bool) ([]models.ToolCall, string, error) {
	calls, err := ParseTextToCalls(text, tools, protocol, stripThink)
	if err != nil {
		return nil, "", err
	}
	if len(calls) > 0 {
		return calls, "", nil
	}
	if IsToolCallTruncated(text) {
		return nil, "truncated", nil
	}
	if text != "" && anyMatch(openMarkerRes, text) {
		return nil, "parse_failed", nil
	}
	return nil, "", nil
}
