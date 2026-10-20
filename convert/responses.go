// Package convert holds canonical ↔ protocol shape converters. Port of
// app/convert.py (Responses API direction).
package convert

import (
	"github.com/Sliverkiss/toolkit/engine"
	"github.com/Sliverkiss/toolkit/jsonx"
	"github.com/Sliverkiss/toolkit/models"
	"github.com/Sliverkiss/toolkit/util"
)

// MapResponsesUsage maps chat-completion usage to Responses usage shape. Port
// of _map_responses_usage.
func MapResponsesUsage(usage map[string]any) map[string]any {
	inputTokens := firstTruthy(usage["prompt_tokens"], usage["input_tokens"])
	outputTokens := firstTruthy(usage["completion_tokens"], usage["output_tokens"])
	if inputTokens == 0 {
		inputTokens = 0
	}
	if outputTokens == 0 {
		outputTokens = 0
	}
	totalTokens := firstTruthy(usage["total_tokens"])
	if totalTokens == 0 {
		totalTokens = inputTokens + outputTokens
	}
	usageOut := map[string]any{
		"input_tokens":  inputTokens,
		"output_tokens": outputTokens,
		"total_tokens":  totalTokens,
	}
	if cached := firstTruthy(usage["cached_tokens"], usage["cache_read_input_tokens"]); cached != 0 {
		usageOut["input_tokens_details"] = map[string]any{"cached_tokens": cached}
	}
	coreKeys := map[string]struct{}{
		"prompt_tokens": {}, "completion_tokens": {}, "total_tokens": {},
		"input_tokens": {}, "output_tokens": {},
	}
	for key, value := range usage {
		if _, core := coreKeys[key]; core {
			continue
		}
		if _, exists := usageOut[key]; exists {
			continue
		}
		usageOut[key] = value
	}
	return usageOut
}

// firstTruthy mirrors Python's `a or b or 0` for JSON scalars.
func firstTruthy(values ...any) int {
	for _, value := range values {
		if jsonx.Falsy(value) {
			continue
		}
		return jsonx.IntValue(value)
	}
	return 0
}

// InputToMessages converts a Responses request body into canonical messages.
// Port of responses_input_to_messages.
func InputToMessages(body map[string]any) []models.Message {
	var messages []models.Message
	if instructions := body["instructions"]; instructions != nil && !jsonx.Falsy(instructions) {
		messages = append(messages, models.Message{Role: "system", Content: jsonx.Str(instructions)})
	}
	rawInput := body["input"]
	if text, ok := rawInput.(string); ok {
		return append(messages, models.Message{Role: "user", Content: text})
	}
	for _, item := range jsonx.AsList(rawInput) {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		itype := jsonx.Str(m["type"])
		role := jsonx.Str(m["role"])
		switch {
		case itype == "message" || role != "":
			if role == "" {
				role = "user"
			}
			var text string
			switch content := m["content"].(type) {
			case string:
				text = content
			case []any:
				var chunks []string
				for _, c := range content {
					switch cv := c.(type) {
					case map[string]any:
						switch cv["type"] {
						case "input_text", "output_text", "text":
							chunks = append(chunks, jsonx.Str(cv["text"]))
						}
					case string:
						chunks = append(chunks, cv)
					}
				}
				text = joinLines(chunks)
			}
			if role != "system" && role != "user" && role != "assistant" {
				role = "user"
			}
			messages = append(messages, models.Message{Role: role, Content: text, Raw: m})
		case itype == "function_call":
			parsed := models.ParseToolArguments(m["arguments"])
			id := firstNonEmpty(jsonx.Str(m["call_id"]), jsonx.Str(m["id"]))
			if id == "" {
				id = util.CallID()
			}
			messages = append(messages, models.Message{
				Role:    "assistant",
				Content: "",
				ToolCalls: []models.ToolCall{{
					ID:        id,
					Name:      jsonx.Str(m["name"]),
					Arguments: parsed,
				}},
				Raw: m,
			})
		case itype == "function_call_output":
			messages = append(messages, models.Message{
				Role:       "tool",
				Content:    jsonx.Str(m["output"]),
				ToolCallID: jsonx.Str(m["call_id"]),
				Raw:        m,
			})
		}
	}
	return messages
}

func joinLines(chunks []string) string {
	out := ""
	for i, chunk := range chunks {
		if i > 0 {
			out += "\n"
		}
		out += chunk
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// ToolsToDefs converts Responses tool declarations into ToolDefs, falling back
// to OpenAI chat shape. Port of responses_tools_to_defs.
func ToolsToDefs(raw any) []models.ToolDef {
	var defs []models.ToolDef
	for _, item := range jsonx.AsList(raw) {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == "function" {
			name := jsonx.Str(m["name"])
			if name == "" {
				continue
			}
			parameters, _ := m["parameters"].(map[string]any)
			if parameters == nil {
				parameters = map[string]any{}
			}
			defs = append(defs, models.ToolDef{
				Name:        name,
				Description: jsonx.Str(m["description"]),
				Parameters:  parameters,
				Raw:         m,
			})
		}
	}
	if len(defs) == 0 {
		return models.OpenAIToolsToDefs(raw)
	}
	return defs
}

// BuildResponsesResponse builds a non-streaming Responses API payload. Port of
// build_responses_response.
func BuildResponsesResponse(model, content string, toolCalls []models.ToolCall, usage map[string]any, promptText string, legacyMap map[string]string) map[string]any {
	output := []any{}
	if content != "" {
		output = append(output, map[string]any{
			"id":     "msg_" + util.RandomID(12),
			"type":   "message",
			"status": "completed",
			"role":   "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": content},
			},
		})
	}
	if len(toolCalls) > 0 {
		for _, item := range engine.ResponsesToolItems(toolCalls, legacyMap) {
			output = append(output, item)
		}
	}
	resolved := util.ResolveUsage(usage, promptText, content)
	return map[string]any{
		"id":         "resp_" + util.RandomID(16),
		"object":     "response",
		"created_at": util.UnixNow(),
		"status":     "completed",
		"model":      model,
		"output":     output,
		"usage":      MapResponsesUsage(resolved),
	}
}
