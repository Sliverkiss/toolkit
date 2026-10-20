// Package adapters converts client protocol bodies to canonical requests and
// canonical responses back to client shapes. Port of app/adapters/.
package adapters

import (
	"errors"

	"github.com/Sliverkiss/toolkit/models"
)

// ErrModelRequired is the 400 raised by both body converters.
var ErrModelRequired = errors.New("model is required")

// BuildCanonical constructs a CanonicalRequest from resolved body parts. Port
// of _build_canonical (the shared T7 helper).
func BuildCanonical(model string, messages []models.Message, tools []models.ToolDef, toolChoice any, stream bool, surface models.Surface, temperature *float64, maxTokens *int, topP *float64, extra map[string]any) *models.CanonicalRequest {
	return &models.CanonicalRequest{
		Model:       model,
		Messages:    messages,
		Tools:       tools,
		ToolChoice:  toolChoice,
		Stream:      stream,
		Surface:     surface,
		Temperature: temperature,
		MaxTokens:   maxTokens,
		TopP:        topP,
		Extra:       extra,
	}
}

// passthroughBodyKeys are chat body fields copied verbatim into req.extra.
var passthroughBodyKeys = []string{
	"temperature", "top_p", "n", "stop", "max_tokens", "max_completion_tokens",
	"presence_penalty", "frequency_penalty", "logit_bias", "user", "seed",
	"response_format", "logprobs", "top_logprobs",
}

func floatPtr(value any) *float64 {
	switch v := value.(type) {
	case float64:
		return &v
	case int:
		f := float64(v)
		return &f
	case int64:
		f := float64(v)
		return &f
	default:
		return nil
	}
}

func intPtr(value any) *int {
	switch v := value.(type) {
	case int:
		return &v
	case int64:
		i := int(v)
		return &i
	case float64:
		i := int(v)
		return &i
	default:
		return nil
	}
}
