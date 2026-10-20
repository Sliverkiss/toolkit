package adapters

import (
	"strings"

	"github.com/Sliverkiss/toolkit/engine"
	"github.com/Sliverkiss/toolkit/fc"
	"github.com/Sliverkiss/toolkit/jsonx"
	"github.com/Sliverkiss/toolkit/models"
	"github.com/Sliverkiss/toolkit/util"
)

// ChatBodyToCanonical converts an OpenAI Chat Completions body. Port of
// openai_chat.body_to_canonical.
func ChatBodyToCanonical(body map[string]any, convertDeveloper bool) (*models.CanonicalRequest, error) {
	model := strings.TrimSpace(jsonx.Str(body["model"]))
	if model == "" {
		return nil, ErrModelRequired
	}
	messages := models.OpenAIResultToMessages(body["messages"], convertDeveloper)
	rawTools := body["tools"]
	if rawTools == nil {
		rawTools = body["functions"]
	}
	tools := models.OpenAIToolsToDefs(rawTools)
	toolChoice, ok := body["tool_choice"]
	if !ok {
		toolChoice = "auto"
	}
	extra := map[string]any{}
	for _, key := range passthroughBodyKeys {
		if value, ok := body[key]; ok {
			extra[key] = value
		}
	}
	maxTokens := body["max_tokens"]
	if maxTokens == nil {
		maxTokens = body["max_completion_tokens"]
	}
	return BuildCanonical(
		model,
		messages,
		tools,
		toolChoice,
		!jsonx.Falsy(body["stream"]),
		models.SurfaceOpenAI,
		floatPtr(body["temperature"]),
		intPtr(maxTokens),
		floatPtr(body["top_p"]),
		extra,
	), nil
}

// OpenAIMessagesFromCanonical renders canonical messages for the upstream wire.
// Port of _openai_messages_from_canonical.
func OpenAIMessagesFromCanonical(req *models.CanonicalRequest) []map[string]any {
	out := make([]map[string]any, 0, len(req.Messages))
	for _, msg := range req.Messages {
		if len(msg.Raw) > 0 {
			copied := make(map[string]any, len(msg.Raw))
			for key, value := range msg.Raw {
				copied[key] = value
			}
			out = append(out, copied)
			continue
		}
		item := map[string]any{"role": msg.Role, "content": msg.Content}
		if msg.Name != "" {
			item["name"] = msg.Name
		}
		if msg.ToolCallID != "" {
			item["tool_call_id"] = msg.ToolCallID
		}
		if len(msg.ToolCalls) > 0 {
			item["tool_calls"] = engine.OpenAIToolCalls(msg.ToolCalls, nil)
		}
		out = append(out, item)
	}
	return out
}

// BuildChatCompletionResponse builds an OpenAI chat.completion payload. Port of
// build_chat_completion_response.
func BuildChatCompletionResponse(model, content string, toolCalls []models.ToolCall, finishReason string, usage map[string]any, promptText string, legacyMap map[string]string) map[string]any {
	message := map[string]any{"role": "assistant", "content": nil}
	if content != "" {
		message["content"] = content
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = engine.OpenAIToolCalls(toolCalls, legacyMap)
		finishReason = "tool_calls"
		if content == "" {
			message["content"] = nil
		}
	}
	resolved := util.ResolveUsage(usage, promptText, content)
	return map[string]any{
		"id":      util.CompletionID(),
		"object":  "chat.completion",
		"created": util.UnixNow(),
		"model":   model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       message,
			"finish_reason": finishReason,
			"logprobs":      nil,
		}},
		"usage": resolved,
	}
}

// ExtractTextAndNativeCalls extracts content, native tool calls, finish reason
// and usage from an upstream chat completion. Port of
// extract_text_and_native_calls.
func ExtractTextAndNativeCalls(upstreamJSON map[string]any, legacyMap map[string]string, tools []map[string]any) (string, []models.ToolCall, string, map[string]any) {
	if legacyMap == nil {
		legacyMap = map[string]string{}
	}
	choices := jsonx.AsList(upstreamJSON["choices"])
	if len(choices) == 0 {
		usage, _ := upstreamJSON["usage"].(map[string]any)
		return "", nil, "stop", usage
	}
	choice, _ := choices[0].(map[string]any)
	if choice == nil {
		choice = map[string]any{}
	}
	message, _ := choice["message"].(map[string]any)
	if message == nil {
		message = map[string]any{}
	}
	content := ""
	if raw := message["content"]; raw != nil {
		if s, ok := raw.(string); ok {
			content = s
		} else {
			content = jsonx.Dumps(raw)
		}
	}
	finish := jsonx.Str(choice["finish_reason"])
	if finish == "" {
		finish = "stop"
	}
	var calls []models.ToolCall
	for _, raw := range jsonx.AsList(message["tool_calls"]) {
		tc, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		fn, _ := tc["function"].(map[string]any)
		if fn == nil {
			fn = map[string]any{}
		}
		parsed := models.ParseToolArguments(fn["arguments"])
		name := jsonx.Str(fn["name"])
		if len(tools) > 0 && name != "" {
			canonicalName := engine.CanonicalizeResponseToolName(name, legacyMap)
			parsed = engine.FilterInputForTool(canonicalName, parsed, tools)
			if coerced, ok := engine.CoerceToolInput(canonicalName, parsed, tools, nil).(map[string]any); ok && coerced != nil {
				parsed = coerced
			}
		}
		calls = append(calls, models.ToolCall{
			ID:        jsonx.Str(tc["id"]),
			Name:      engine.CanonicalizeResponseToolName(name, legacyMap),
			Arguments: parsed,
		})
	}
	usage, _ := upstreamJSON["usage"].(map[string]any)
	return content, calls, finish, usage
}

// ChatRequestBodyKeys reports the passthrough keys (used by tests).
func ChatRequestBodyKeys() []string { return append([]string(nil), passthroughBodyKeys...) }

// ensure fc stays referenced for the handler layer moved to engine/orchestrator.
var _ = fc.DefaultProtocol
