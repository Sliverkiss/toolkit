package adapters

import (
	"strings"

	"github.com/Sliverkiss/toolkit/convert"
	"github.com/Sliverkiss/toolkit/jsonx"
	"github.com/Sliverkiss/toolkit/models"
)

// ResponsesBodyToCanonical converts an OpenAI Responses API body. Port of
// openai_responses.body_to_canonical.
func ResponsesBodyToCanonical(body map[string]any) (*models.CanonicalRequest, error) {
	model := strings.TrimSpace(jsonx.Str(body["model"]))
	if model == "" {
		return nil, ErrModelRequired
	}
	messages := convert.InputToMessages(body)
	tools := convert.ToolsToDefs(body["tools"])
	toolChoice, ok := body["tool_choice"]
	if !ok {
		toolChoice = "auto"
	}
	extra := map[string]any{}
	for _, key := range []string{"metadata", "store", "user"} {
		if value, ok := body[key]; ok {
			extra[key] = value
		}
	}
	maxTokens := body["max_output_tokens"]
	if maxTokens == nil {
		maxTokens = body["max_tokens"]
	}
	return BuildCanonical(
		model,
		messages,
		tools,
		toolChoice,
		!jsonx.Falsy(body["stream"]),
		models.SurfaceResponses,
		floatPtr(body["temperature"]),
		intPtr(maxTokens),
		floatPtr(body["top_p"]),
		extra,
	), nil
}
