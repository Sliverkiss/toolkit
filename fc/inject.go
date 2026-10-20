package fc

import (
	"strings"

	"github.com/Sliverkiss/toolkit/engine"
	"github.com/Sliverkiss/toolkit/jsonx"
	"github.com/Sliverkiss/toolkit/models"
)

// ToolsForSDK normalizes registered tools into the SDK shape. Port of
// tools_for_sdk.
func ToolsForSDK(tools []models.ToolDef) []map[string]any {
	return engine.NormalizeTools(models.ToolDefsToOpenAI(tools))
}

// BuildInstructions renders the XYML instruction block for a tool list. Port of
// build_instructions.
func BuildInstructions(tools []models.ToolDef, protocol string) string {
	return engine.BuildToolInstructions(ToolsForSDK(tools), Config(protocol, ""), nil)
}

// Inject returns the instruction block for the tools (public entry point
// requested by the task: fc.Inject(systemPrompt, tools)).
func Inject(systemPrompt string, tools []models.ToolDef) string {
	if strings.TrimSpace(systemPrompt) == "" {
		return BuildInstructions(tools, DefaultProtocol)
	}
	return systemPrompt + "\n\n" + BuildInstructions(tools, DefaultProtocol)
}

// ContentToText flattens message content into plain text. Port of
// _content_to_text.
func ContentToText(content any) string {
	if content == nil {
		return ""
	}
	if s, ok := content.(string); ok {
		return s
	}
	if list, ok := content.([]any); ok {
		parts := make([]string, 0, len(list))
		for _, item := range list {
			switch v := item.(type) {
			case map[string]any:
				if v["type"] == "text" || v["text"] != nil {
					if text := jsonx.Str(v["text"]); text != "" {
						parts = append(parts, text)
					}
				}
			case string:
				parts = append(parts, v)
			}
		}
		nonEmpty := parts[:0]
		for _, part := range parts {
			if part != "" {
				nonEmpty = append(nonEmpty, part)
			}
		}
		return strings.Join(nonEmpty, "\n")
	}
	return jsonx.Str(content)
}

// RenderHistoryMessages flattens OpenAI-style tool history into plain chat
// messages. Port of render_history_messages.
func RenderHistoryMessages(messages []models.Message, protocol string) []map[string]any {
	cfg := Config(protocol, "")
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		switch {
		case msg.Role == "tool":
			toolID := msg.ToolCallID
			if toolID == "" {
				toolID = "unknown"
			}
			body := ContentToText(msg.Content)
			header := "[Tool Result id=" + toolID
			if msg.Name != "" {
				header += " name=" + msg.Name
			}
			header += "]"
			out = append(out, map[string]any{"role": "user", "content": header + "\n" + body})
		case msg.Role == "assistant" && len(msg.ToolCalls) > 0:
			var blocks []string
			if text := strings.TrimSpace(ContentToText(msg.Content)); text != "" {
				blocks = append(blocks, text)
			}
			for _, call := range msg.ToolCalls {
				blocks = append(blocks, engine.RenderToolCall(call.Name, call.Arguments, cfg, nil))
			}
			out = append(out, map[string]any{"role": "assistant", "content": strings.Join(blocks, "\n")})
		default:
			role := msg.Role
			if role != "system" && role != "user" && role != "assistant" {
				role = "user"
			}
			content := ContentToText(msg.Content)
			if role == "system" && strings.TrimSpace(content) == "" {
				continue
			}
			out = append(out, map[string]any{"role": role, "content": content})
		}
	}
	return out
}

// InjectPromptMessages builds the upstream OpenAI chat messages for prompt FC.
// Port of inject_prompt_messages.
func InjectPromptMessages(messages []models.Message, tools []models.ToolDef, protocol string, convertDeveloperToSystem bool) []map[string]any {
	instructions := BuildInstructions(tools, protocol)
	history := RenderHistoryMessages(messages, protocol)
	if len(history) > 0 && history[0]["role"] == "system" {
		history[0]["content"] = strings.TrimSpace(jsonx.Str(history[0]["content"]) + "\n\n" + instructions)
		return history
	}
	prepended := make([]map[string]any, 0, len(history)+1)
	prepended = append(prepended, map[string]any{"role": "system", "content": instructions})
	return append(prepended, history...)
}

// StripToolsFromOpenAIBody removes request-side tool fields for prompt FC.
// Port of strip_tools_from_openai_body.
func StripToolsFromOpenAIBody(body map[string]any) map[string]any {
	cleaned := make(map[string]any, len(body))
	for key, value := range body {
		cleaned[key] = value
	}
	for _, key := range []string{"tools", "tool_choice", "functions", "function_call", "parallel_tool_calls"} {
		delete(cleaned, key)
	}
	return cleaned
}
