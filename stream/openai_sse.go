package stream

import (
	"encoding/json"
	"strings"

	"github.com/Sliverkiss/toolkit/engine"
	"github.com/Sliverkiss/toolkit/fc"
	"github.com/Sliverkiss/toolkit/jsonx"
	"github.com/Sliverkiss/toolkit/models"
	"github.com/Sliverkiss/toolkit/reqlog"
	"github.com/Sliverkiss/toolkit/util"
)

// Emit is the sink for SSE frames.
type Emit func(frame string) error

// Emitter is a stream body: it pushes frames into an Emit sink.
type Emitter func(emit Emit) error

// logUpstreamText persists the model's original text (incl. consumed markup).
func logUpstreamText(requestID string, parts []string) {
	if requestID != "" && len(parts) > 0 {
		reqlog.Write(requestID, "upstream", strings.Join(parts, ""))
	}
}

// chunk builds a streaming delta chunk. Port of _chunk (do not merge with
// usageChunk — wire formats differ).
func chunk(model string, delta map[string]any, finishReason any, chunkID string, usage map[string]any) map[string]any {
	chunk := map[string]any{
		"id":      chunkID,
		"object":  "chat.completion.chunk",
		"created": util.UnixNow(),
		"model":   model,
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         delta,
			"finish_reason": finishReason,
			"logprobs":      nil,
		}},
	}
	if usage != nil {
		chunk["usage"] = usage
	}
	return chunk
}

func newChunk(model string, delta map[string]any, finishReason any, chunkID string) map[string]any {
	id := chunkID
	if id == "" {
		id = util.CompletionID()
	}
	return chunk(model, delta, finishReason, id, nil)
}

// usageChunk is the OpenAI-standard usage-only chunk (empty choices).
func usageChunk(model, chunkID string, usage map[string]any) map[string]any {
	return map[string]any{
		"id":      chunkID,
		"object":  "chat.completion.chunk",
		"created": util.UnixNow(),
		"model":   model,
		"choices": []any{},
		"usage":   usage,
	}
}

func emitFinalUsageFrames(emit Emit, model, chunkID string, usage map[string]any) error {
	if err := emit(util.FormatSSE(usageChunk(model, chunkID, usage), "")); err != nil {
		return err
	}
	return emit(util.DoneFrame())
}

// canonicalizePayloadToolNames rewrites native tool names in place (legacy
// spelling -> registered name). Port of _canonicalize_payload_tool_names.
func canonicalizePayloadToolNames(payload map[string]any, legacyMap map[string]string) {
	for _, rawChoice := range jsonx.AsList(payload["choices"]) {
		choice, ok := rawChoice.(map[string]any)
		if !ok {
			continue
		}
		for _, key := range []string{"delta", "message"} {
			container, ok := choice[key].(map[string]any)
			if !ok {
				continue
			}
			for _, rawTC := range jsonx.AsList(container["tool_calls"]) {
				tc, ok := rawTC.(map[string]any)
				if !ok {
					continue
				}
				fn, ok := tc["function"].(map[string]any)
				if !ok {
					continue
				}
				fn["name"] = engine.CanonicalizeResponseToolName(jsonx.Str(fn["name"]), legacyMap)
			}
		}
	}
}

// CoercePayloadToolArguments coerces complete JSON tool arguments by schema.
// Port of _coerce_payload_tool_arguments.
func CoercePayloadToolArguments(payload map[string]any, tools []map[string]any) {
	if len(tools) == 0 {
		return
	}
	for _, rawChoice := range jsonx.AsList(payload["choices"]) {
		choice, ok := rawChoice.(map[string]any)
		if !ok {
			continue
		}
		for _, key := range []string{"delta", "message"} {
			container, ok := choice[key].(map[string]any)
			if !ok {
				continue
			}
			for _, rawTC := range jsonx.AsList(container["tool_calls"]) {
				tc, ok := rawTC.(map[string]any)
				if !ok {
					continue
				}
				fn, ok := tc["function"].(map[string]any)
				if !ok {
					continue
				}
				args, ok := fn["arguments"].(string)
				if !ok || strings.TrimSpace(args) == "" {
					continue
				}
				decoded, ok := jsonx.TryJSONValue(args)
				if !ok {
					continue
				}
				parsed, ok := decoded.(map[string]any)
				if !ok {
					continue
				}
				canonicalName := jsonx.Str(fn["name"])
				filtered := engine.FilterInputForTool(canonicalName, parsed, tools)
				coerced, ok := engine.CoerceToolInput(canonicalName, filtered, tools, nil).(map[string]any)
				if !ok || mapsEqual(coerced, parsed) {
					continue
				}
				fn["arguments"] = jsonx.Dumps(coerced)
			}
		}
	}
}

func mapsEqual(a, b map[string]any) bool {
	return jsonx.Dumps(a) == jsonx.Dumps(b)
}

// StreamNativePassthrough forwards upstream OpenAI SSE with proper framing.
// Port of stream_native_passthrough.
func StreamNativePassthrough(source util.LineSource, emit Emit, model, promptText string, legacyMap map[string]string, tools []map[string]any) error {
	if legacyMap == nil {
		legacyMap = map[string]string{}
	}
	finished := false
	chunkID := util.CompletionID()
	var contentParts []string
	var upstreamUsage map[string]any
	it := newEventIterator(source)
	for {
		event, ok, err := it.Next()
		if err != nil {
			return emitStreamError(emit, model, chunkID, err, promptText, contentParts)
		}
		if !ok {
			break
		}
		if event.IsDone() {
			finished = true
			if err := emitFinalUsageFrames(emit, model, chunkID,
				util.ResolveUsage(upstreamUsage, promptText, strings.Join(contentParts, ""))); err != nil {
				return err
			}
			return nil
		}
		payload := event.JSON()
		if payload == nil {
			if event.Data != "" {
				if err := emit(util.FormatSSE(event.Data, event.Event)); err != nil {
					return err
				}
			}
			continue
		}
		if model != "" && jsonx.Str(payload["model"]) != model {
			payload["model"] = model
		}
		if len(legacyMap) > 0 {
			canonicalizePayloadToolNames(payload, legacyMap)
		}
		if len(tools) > 0 {
			CoercePayloadToolArguments(payload, tools)
		}
		if usage, ok := payload["usage"].(map[string]any); ok {
			upstreamUsage = usage
		}
		if choices := jsonx.AsList(payload["choices"]); len(choices) > 0 {
			if choice, ok := choices[0].(map[string]any); ok {
				if delta, ok := choice["delta"].(map[string]any); ok {
					if piece := delta["content"]; !jsonx.Falsy(piece) {
						contentParts = append(contentParts, jsonx.Str(piece))
					}
				}
			}
		}
		if err := emit(util.FormatSSE(payload, event.Event)); err != nil {
			return err
		}
	}
	if !finished {
		return emitFinalUsageFrames(emit, model, chunkID,
			util.ResolveUsage(upstreamUsage, promptText, strings.Join(contentParts, "")))
	}
	return nil
}

func emitStreamError(emit Emit, model, chunkID string, streamErr error, promptText string, contentParts []string) error {
	if err := emit(util.FormatSSE(newChunk(model,
		map[string]any{"content": "\n[stream error: " + streamErr.Error() + "]"}, "stop", chunkID), "")); err != nil {
		return err
	}
	return emitFinalUsageFrames(emit, model, chunkID,
		util.ResolveUsage(nil, promptText, strings.Join(contentParts, "")))
}

// StreamPromptFC parses a streamed XYML response into OpenAI tool_calls. Port of
// stream_prompt_fc.
func StreamPromptFC(source util.LineSource, emit Emit, model string, tools []models.ToolDef, protocol string, stripThink bool, promptText string, legacyMap map[string]string, requestID string) error {
	sieve := fc.CreateSieve(tools, protocol, 96)
	chunkID := util.CompletionID()
	if err := emit(util.FormatSSE(newChunk(model, map[string]any{"role": "assistant"}, nil, chunkID), "")); err != nil {
		return err
	}
	var fullTextParts []string
	emittedCalls := false
	var upstreamUsage map[string]any

	finalUsage := func() map[string]any {
		return util.ResolveUsage(upstreamUsage, promptText, strings.Join(fullTextParts, ""))
	}
	emitCalls := func(calls []models.ToolCall) error {
		if len(calls) == 0 {
			return nil
		}
		emittedCalls = true
		openaiCalls := engine.OpenAIToolCalls(calls, legacyMap)
		for index, tc := range openaiCalls {
			fn, _ := tc["function"].(map[string]any)
			arguments := ""
			if fn != nil {
				if s, ok := fn["arguments"].(string); ok {
					arguments = s
				}
			}
			frame := util.FormatSSE(newChunk(model, map[string]any{
				"tool_calls": []any{map[string]any{
					"index": index,
					"id":    tc["id"],
					"type":  "function",
					"function": map[string]any{
						"name":      fn["name"],
						"arguments": arguments,
					},
				}},
			}, nil, chunkID), "")
			if err := emit(frame); err != nil {
				return err
			}
		}
		if err := emit(util.FormatSSE(newChunk(model, map[string]any{}, "tool_calls", chunkID), "")); err != nil {
			return err
		}
		return emitFinalUsageFrames(emit, model, chunkID, finalUsage())
	}

	it := newEventIterator(source)
	for {
		event, ok, err := it.Next()
		if err != nil {
			return emitStreamError(emit, model, chunkID, err, promptText, fullTextParts)
		}
		if !ok || event.IsDone() {
			break
		}
		payload := event.JSON()
		if payload == nil {
			continue
		}
		if usage, ok := payload["usage"].(map[string]any); ok {
			upstreamUsage = usage
		}
		choices := jsonx.AsList(payload["choices"])
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]any)
		delta, _ := choice["delta"].(map[string]any)
		piece := any(nil)
		if delta != nil {
			piece = delta["content"]
		}
		if jsonx.Falsy(piece) {
			if message, ok := choice["message"].(map[string]any); ok {
				piece = message["content"]
			}
		}
		if jsonx.Falsy(piece) {
			continue
		}
		text := jsonx.Str(piece)
		fullTextParts = append(fullTextParts, text)
		for _, sev := range sieve.ProcessChunk(text) {
			switch sev.Type {
			case "content":
				if sev.Text != "" {
					if err := emit(util.FormatSSE(newChunk(model, map[string]any{"content": sev.Text}, nil, chunkID), "")); err != nil {
						return err
					}
				}
			case "tool_calls":
				logUpstreamText(requestID, fullTextParts)
				return emitCalls(fc.ParsedCallsToToolCalls(sev.Calls))
			}
		}
	}
	for _, sev := range sieve.Flush() {
		switch sev.Type {
		case "content":
			if sev.Text != "" {
				if err := emit(util.FormatSSE(newChunk(model, map[string]any{"content": sev.Text}, nil, chunkID), "")); err != nil {
					return err
				}
			}
		case "tool_calls":
			if !emittedCalls {
				logUpstreamText(requestID, fullTextParts)
				return emitCalls(fc.ParsedCallsToToolCalls(sev.Calls))
			}
		}
	}
	if !emittedCalls {
		fullText := strings.Join(fullTextParts, "")
		calls, err := fc.ParseTextToCalls(fullText, tools, protocol, stripThink)
		if err != nil {
			return emitStreamError(emit, model, chunkID, err, promptText, fullTextParts)
		}
		if len(calls) > 0 {
			logUpstreamText(requestID, fullTextParts)
			return emitCalls(calls)
		}
		if err := emit(util.FormatSSE(newChunk(model, map[string]any{}, "stop", chunkID), "")); err != nil {
			return err
		}
	}
	logUpstreamText(requestID, fullTextParts)
	return emitFinalUsageFrames(emit, model, chunkID, finalUsage())
}

// ParseSSEFrames decodes frame strings into payload objects (test helper shared
// by the ported suites).
func ParseSSEFrames(frames []string) []map[string]any {
	var out []map[string]any
	for _, frame := range frames {
		for _, line := range strings.Split(frame, "\n") {
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(line[5:])
			if data == "[DONE]" {
				continue
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(data), &payload); err != nil {
				continue
			}
			out = append(out, payload)
		}
	}
	return out
}
