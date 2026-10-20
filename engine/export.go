package engine

import (
	"github.com/Sliverkiss/toolkit/jsonx"
	"github.com/Sliverkiss/toolkit/models"
)

// OpenAIToolCalls renders calls in OpenAI function shape. Port of
// openai_tool_calls.
func OpenAIToolCalls(calls any, legacyMap map[string]string) []map[string]any {
	if legacyMap == nil {
		legacyMap = map[string]string{}
	}
	nativeCalls := toNativeCalls(calls)
	out := make([]map[string]any, 0, len(nativeCalls))
	for _, call := range nativeCalls {
		out = append(out, map[string]any{
			"id":   call.ID,
			"type": "function",
			"function": map[string]any{
				"name":      CanonicalizeResponseToolName(call.Name, legacyMap),
				"arguments": ArgumentsString(call.Arguments),
			},
		})
	}
	return out
}

// ResponsesToolItems renders calls as Responses API function_call items. Port of
// responses_tool_items.
func ResponsesToolItems(calls any, legacyMap map[string]string) []map[string]any {
	if legacyMap == nil {
		legacyMap = map[string]string{}
	}
	nativeCalls := toNativeCalls(calls)
	out := make([]map[string]any, 0, len(nativeCalls))
	for _, call := range nativeCalls {
		out = append(out, map[string]any{
			"id":        "fc_" + randomID(12),
			"type":      "function_call",
			"status":    "completed",
			"call_id":   call.ID,
			"name":      CanonicalizeResponseToolName(call.Name, legacyMap),
			"arguments": ArgumentsString(call.Arguments),
		})
	}
	return out
}

func toNativeCalls(calls any) []models.ToolCall {
	switch v := calls.(type) {
	case []models.ToolCall:
		return v
	case []ParsedToolCall:
		out := make([]models.ToolCall, 0, len(v))
		for _, call := range v {
			out = append(out, models.ToolCall{ID: call.ID, Name: call.Name, Arguments: asMap(call.Input)})
		}
		return out
	default:
		return nil
	}
}

// ArgumentsString renders arguments as a JSON string unless already a string.
func ArgumentsString(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	if value == nil {
		return "{}"
	}
	return jsonx.Dumps(value)
}

// InputString renders a parsed call input as a JSON argument string.
func InputString(input any) string { return ArgumentsString(input) }

func asMap(value any) map[string]any {
	if m, ok := value.(map[string]any); ok {
		return m
	}
	if value == nil {
		return map[string]any{}
	}
	return map[string]any{"value": value}
}
