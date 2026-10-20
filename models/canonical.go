// Package models holds protocol-neutral request/response types: the Go port of
// app/models/canonical.py.
package models

import (
	"strings"

	"github.com/Sliverkiss/toolkit/jsonx"
)

// Surface is the client-facing protocol shape.
type Surface string

// FCMode is the requested (client-side) function-calling mode.
type FCMode string

// ResolvedFCMode is the concrete path chosen for one request.
type ResolvedFCMode string

const (
	SurfaceOpenAI    Surface = "openai"
	SurfaceResponses Surface = "responses"

	FCModeNative FCMode = "native"
	FCModePrompt FCMode = "prompt"
	FCModeAuto   FCMode = "auto"

	ResolvedNative      ResolvedFCMode = "native"
	ResolvedPrompt      ResolvedFCMode = "prompt"
	ResolvedPassthrough ResolvedFCMode = "passthrough"
)

// ToolCall is one normalized call. Arguments is always a map after parsing.
type ToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// ToolDef is one registered tool.
type ToolDef struct {
	Name        string
	Description string
	Parameters  map[string]any
	Raw         map[string]any
}

// Message is a canonical chat message.
type Message struct {
	Role       string
	Content    any
	Name       string
	ToolCallID string
	ToolCalls  []ToolCall
	Raw        map[string]any
}

// CanonicalRequest is the protocol-neutral request.
type CanonicalRequest struct {
	Model             string
	Messages          []Message
	Tools             []ToolDef
	ToolChoice        any
	Stream            bool
	Surface           Surface
	Temperature       *float64
	MaxTokens         *int
	TopP              *float64
	Extra             map[string]any
	FCMode            ResolvedFCMode
	UpstreamModel     string
	RequestFCOverride string
}

// CanonicalResponse is the protocol-neutral upstream result.
type CanonicalResponse struct {
	Model        string
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
	Usage        map[string]any
	Raw          map[string]any
}

// ToolDefsToOpenAI renders tool definitions in OpenAI function shape. Tools that
// carry Raw are forwarded verbatim (Python: `if tool.raw: out.append(tool.raw)`).
func ToolDefsToOpenAI(tools []ToolDef) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if len(tool.Raw) > 0 {
			out = append(out, tool.Raw)
			continue
		}
		params := tool.Parameters
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  params,
			},
		})
	}
	return out
}

// OpenAIToolsToDefs parses OpenAI chat tools (nested `function`) and flat
// Anthropic-ish tool objects (`name` + `parameters`/`input_schema`).
func OpenAIToolsToDefs(rawTools any) []ToolDef {
	items := asList(rawTools)
	if len(items) == 0 {
		return nil
	}
	var defs []ToolDef
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == "function" {
			if fn, ok := m["function"].(map[string]any); ok {
				params, _ := fn["parameters"].(map[string]any)
				if params == nil {
					params = map[string]any{}
				}
				defs = append(defs, ToolDef{
					Name:        str(fn["name"]),
					Description: str(fn["description"]),
					Parameters:  params,
					Raw:         m,
				})
				continue
			}
		}
		if str(m["name"]) != "" {
			params, _ := m["parameters"].(map[string]any)
			if params == nil {
				params, _ = m["input_schema"].(map[string]any)
			}
			if params == nil {
				params = map[string]any{}
			}
			defs = append(defs, ToolDef{
				Name:        str(m["name"]),
				Description: str(m["description"]),
				Parameters:  params,
				Raw:         m,
			})
		}
	}
	out := defs[:0]
	for _, d := range defs {
		if d.Name != "" {
			out = append(out, d)
		}
	}
	return out
}

// ParseToolArguments normalizes tool-call arguments. Port of _parse_tool_arguments.
func ParseToolArguments(args any) map[string]any {
	switch v := args.(type) {
	case map[string]any:
		return v
	case string:
		if strings.TrimSpace(v) == "" {
			return map[string]any{}
		}
		value, ok := jsonx.TryJSONValue(v)
		if !ok {
			return map[string]any{"_raw": v}
		}
		if m, ok := value.(map[string]any); ok {
			return m
		}
		return map[string]any{"value": value}
	}
	return map[string]any{}
}

// OpenAIResultToMessages converts OpenAI chat messages into canonical messages.
// Port of openai_messages_to_canonical.
func OpenAIResultToMessages(rawMessages any, convertDeveloper bool) []Message {
	var messages []Message
	for _, item := range asList(rawMessages) {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role := str(m["role"])
		if role == "" {
			role = "user"
		}
		if convertDeveloper && role == "developer" {
			role = "system"
		}
		var calls []ToolCall
		for _, tc := range asList(m["tool_calls"]) {
			tcMap, ok := tc.(map[string]any)
			if !ok {
				continue
			}
			fn, _ := tcMap["function"].(map[string]any)
			calls = append(calls, ToolCall{
				ID:        str(tcMap["id"]),
				Name:      str(fn["name"]),
				Arguments: ParseToolArguments(fn["arguments"]),
			})
		}
		msg := Message{
			Role:       role,
			Content:    m["content"],
			Name:       str(m["name"]),
			ToolCallID: str(m["tool_call_id"]),
			ToolCalls:  calls,
			Raw:        m,
		}
		if _, has := m["content"]; !has {
			msg.Content = ""
		}
		messages = append(messages, msg)
	}
	return messages
}

func asList(value any) []any { return jsonx.AsList(value) }

func str(value any) string { return jsonx.Str(value) }
