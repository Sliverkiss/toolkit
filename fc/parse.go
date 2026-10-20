package fc

import (
	"regexp"

	"github.com/Sliverkiss/toolkit/engine"
	"github.com/Sliverkiss/toolkit/models"
)

var thinkRe = regexp.MustCompile(`(?is)<think>[\s\S]*?</think>`)

// StripThinkTags removes <think>…</think> blocks. Port of strip_think_tags.
func StripThinkTags(text string) string {
	if text == "" {
		return ""
	}
	return thinkRe.ReplaceAllString(text, "")
}

// ParsedToToolCall converts an engine ParsedToolCall into a models.ToolCall.
// Port of parsed_to_toolcall.
func ParsedToToolCall(item engine.ParsedToolCall) models.ToolCall {
	arguments, ok := item.Input.(map[string]any)
	if !ok {
		if item.Input == nil {
			arguments = map[string]any{}
		} else {
			arguments = map[string]any{"value": item.Input}
		}
	}
	id := item.ID
	if id == "" {
		id = ""
	}
	return models.ToolCall{ID: id, Name: item.Name, Arguments: arguments}
}

// ParsedCallsToToolCalls maps a parsed-call slice into normalized ToolCalls.
// Port of parsed_calls_to_toolcalls.
func ParsedCallsToToolCalls(calls []engine.ParsedToolCall) []models.ToolCall {
	out := make([]models.ToolCall, 0, len(calls))
	for _, item := range calls {
		out = append(out, ParsedToToolCall(item))
	}
	return out
}

// ParseTextToCalls parses model text into ToolCalls. Port of
// parse_text_to_calls.
func ParseTextToCalls(text string, tools []models.ToolDef, protocol string, stripThink bool) ([]models.ToolCall, error) {
	source := text
	if stripThink {
		source = StripThinkTags(text)
	}
	parsed, err := engine.ParseToolCalls(source, ToolsForSDK(tools), Config(protocol, ""))
	if err != nil {
		return nil, err
	}
	return ParsedCallsToToolCalls(parsed), nil
}

// Parse is the public entry point fc.Parse(text, tools).
func Parse(text string, tools []models.ToolDef) ([]models.ToolCall, error) {
	return ParseTextToCalls(text, tools, DefaultProtocol, true)
}

// ToOpenAIToolCalls renders ToolCalls in OpenAI function shape. Port of
// to_openai_tool_calls.
func ToOpenAIToolCalls(calls []models.ToolCall, legacyMap map[string]string) []map[string]any {
	return engine.OpenAIToolCalls(calls, legacyMap)
}

// CreateSieve builds a streaming sieve over the prompt-FC config. Port of
// create_sieve.
func CreateSieve(tools []models.ToolDef, protocol string, holdLength int) *engine.ToolSieve {
	if holdLength <= 0 {
		holdLength = 96
	}
	return engine.NewToolSieve(ToolsForSDK(tools), Config(protocol, ""), holdLength)
}
