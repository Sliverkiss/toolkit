package stream

import (
	"strings"

	"github.com/Sliverkiss/toolkit/convert"
	"github.com/Sliverkiss/toolkit/engine"
	"github.com/Sliverkiss/toolkit/fc"
	"github.com/Sliverkiss/toolkit/jsonx"
	"github.com/Sliverkiss/toolkit/models"
	"github.com/Sliverkiss/toolkit/util"
)

// ResponsesStreamState is the event state machine state. Port of
// ResponsesStreamState.
type ResponsesStreamState struct {
	ResponseID       string
	Model            string
	Sequence         int
	OutputIndex      int
	CreatedAt        int64
	TextItemID       string
	TextContentIndex int
	TextStarted      bool
	FullText         string
	ToolBuffers      map[int]map[string]string
	ToolItemIDs      map[int]string
	EmittedIndexes   map[int]struct{}
	Completed        bool
}

// NewResponsesStreamState creates a state with fresh ids.
func NewResponsesStreamState(model string) *ResponsesStreamState {
	return &ResponsesStreamState{
		ResponseID:     "resp_" + util.RandomID(16),
		Model:          model,
		CreatedAt:      util.UnixNow(),
		ToolBuffers:    map[int]map[string]string{},
		ToolItemIDs:    map[int]string{},
		EmittedIndexes: map[int]struct{}{},
	}
}

func (s *ResponsesStreamState) nextSeq() int {
	s.Sequence++
	return s.Sequence
}

func (s *ResponsesStreamState) baseResponse(status string) map[string]any {
	return map[string]any{
		"id":         s.ResponseID,
		"object":     "response",
		"created_at": s.CreatedAt,
		"status":     status,
		"model":      s.Model,
		"output":     []any{},
		"usage":      nil,
	}
}

// EmitEvent frames one Responses event with its type in body and event line.
// Port of emit_event.
func EmitEvent(eventType string, payload map[string]any) string {
	body := make(map[string]any, len(payload)+1)
	for key, value := range payload {
		body[key] = value
	}
	body["type"] = eventType
	return util.FormatSSE(body, eventType)
}

// StartEvents returns response.created and response.in_progress.
func StartEvents(state *ResponsesStreamState) []string {
	return []string{
		EmitEvent("response.created", map[string]any{
			"response":        state.baseResponse("in_progress"),
			"sequence_number": state.nextSeq(),
		}),
		EmitEvent("response.in_progress", map[string]any{
			"response":        state.baseResponse("in_progress"),
			"sequence_number": state.nextSeq(),
		}),
	}
}

// EnsureTextItem emits the message/output_text part once text starts.
func EnsureTextItem(state *ResponsesStreamState) []string {
	if state.TextStarted {
		return nil
	}
	state.TextStarted = true
	state.TextItemID = "msg_" + util.RandomID(12)
	item := map[string]any{
		"id": state.TextItemID, "type": "message", "status": "in_progress",
		"role": "assistant", "content": []any{},
	}
	return []string{
		EmitEvent("response.output_item.added", map[string]any{
			"output_index": state.OutputIndex, "item": item, "sequence_number": state.nextSeq(),
		}),
		EmitEvent("response.content_part.added", map[string]any{
			"item_id": state.TextItemID, "output_index": state.OutputIndex,
			"content_index":   state.TextContentIndex,
			"part":            map[string]any{"type": "output_text", "text": ""},
			"sequence_number": state.nextSeq(),
		}),
	}
}

// TextDeltaEvents emits output_text.delta frames.
func TextDeltaEvents(state *ResponsesStreamState, delta string) []string {
	if delta == "" {
		return nil
	}
	out := EnsureTextItem(state)
	state.FullText += delta
	out = append(out, EmitEvent("response.output_text.delta", map[string]any{
		"item_id": state.TextItemID, "output_index": state.OutputIndex,
		"content_index": state.TextContentIndex, "delta": delta,
		"sequence_number": state.nextSeq(),
	}))
	return out
}

// FinishTextItem closes the message item.
func FinishTextItem(state *ResponsesStreamState) []string {
	if !state.TextStarted || state.TextItemID == "" {
		return nil
	}
	out := []string{
		EmitEvent("response.output_text.done", map[string]any{
			"item_id": state.TextItemID, "output_index": state.OutputIndex,
			"content_index": state.TextContentIndex, "text": state.FullText,
			"sequence_number": state.nextSeq(),
		}),
		EmitEvent("response.content_part.done", map[string]any{
			"item_id": state.TextItemID, "output_index": state.OutputIndex,
			"content_index":   state.TextContentIndex,
			"part":            map[string]any{"type": "output_text", "text": state.FullText},
			"sequence_number": state.nextSeq(),
		}),
		EmitEvent("response.output_item.done", map[string]any{
			"output_index": state.OutputIndex,
			"item": map[string]any{
				"id": state.TextItemID, "type": "message", "status": "completed",
				"role":    "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": state.FullText}},
			},
			"sequence_number": state.nextSeq(),
		}),
	}
	state.OutputIndex++
	return out
}

// EnsureToolItem emits the function_call item once per index.
func EnsureToolItem(state *ResponsesStreamState, index int, callIDValue, name string) []string {
	if _, ok := state.EmittedIndexes[index]; ok {
		return nil
	}
	state.EmittedIndexes[index] = struct{}{}
	itemID := "fc_" + util.RandomID(12)
	state.ToolItemIDs[index] = itemID
	callID := callIDValue
	if callID == "" {
		callID = util.CallID()
	}
	item := map[string]any{
		"id": itemID, "type": "function_call", "status": "in_progress",
		"call_id": callID, "name": name, "arguments": "",
	}
	return []string{
		EmitEvent("response.output_item.added", map[string]any{
			"output_index": state.OutputIndex + index, "item": item,
			"sequence_number": state.nextSeq(),
		}),
	}
}

// ToolArgsDelta accumulates and emits function-call argument deltas.
func ToolArgsDelta(state *ResponsesStreamState, index int, callIDValue, name, argsDelta string) []string {
	out := EnsureToolItem(state, index, callIDValue, name)
	buf, ok := state.ToolBuffers[index]
	if !ok {
		buf = map[string]string{"id": callIDValue, "name": name, "arguments": ""}
		state.ToolBuffers[index] = buf
	}
	if callIDValue != "" {
		buf["id"] = callIDValue
	}
	if name != "" {
		buf["name"] = name
	}
	if argsDelta != "" {
		buf["arguments"] = buf["arguments"] + argsDelta
		itemID := state.ToolItemIDs[index]
		if itemID == "" {
			itemID = "fc_" + util.RandomID(12)
		}
		out = append(out, EmitEvent("response.function_call_arguments.delta", map[string]any{
			"item_id": itemID, "output_index": state.OutputIndex + index,
			"delta": argsDelta, "sequence_number": state.nextSeq(),
		}))
	}
	return out
}

// FinishToolItems closes every accumulated function-call item.
func FinishToolItems(state *ResponsesStreamState) []string {
	var out []string
	for _, index := range sortedIntKeys(state.ToolBuffers) {
		buf := state.ToolBuffers[index]
		itemID := state.ToolItemIDs[index]
		if itemID == "" {
			itemID = "fc_" + util.RandomID(12)
		}
		args := buf["arguments"]
		callID := buf["id"]
		if callID == "" {
			callID = util.CallID()
		}
		out = append(out,
			EmitEvent("response.function_call_arguments.done", map[string]any{
				"item_id": itemID, "output_index": state.OutputIndex,
				"arguments": args, "sequence_number": state.nextSeq(),
			}),
			EmitEvent("response.output_item.done", map[string]any{
				"output_index": state.OutputIndex,
				"item": map[string]any{
					"id": itemID, "type": "function_call", "status": "completed",
					"call_id": callID, "name": buf["name"], "arguments": args,
				},
				"sequence_number": state.nextSeq(),
			}),
		)
		state.OutputIndex++
	}
	return out
}

func sortedIntKeys(m map[int]map[string]string) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// CompleteEvents emits response.completed (or response.failed). Port of
// complete_events.
func CompleteEvents(state *ResponsesStreamState, usage map[string]any, errorMessage, promptText, completionText string) []string {
	if state.Completed {
		return nil
	}
	state.Completed = true
	if errorMessage != "" {
		response := state.baseResponse("failed")
		response["error"] = map[string]any{"message": errorMessage}
		return []string{EmitEvent("response.failed", map[string]any{
			"response": response, "sequence_number": state.nextSeq(),
		})}
	}
	output := []any{}
	if state.TextStarted {
		output = append(output, map[string]any{
			"id": state.TextItemID, "type": "message", "status": "completed",
			"role":    "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": state.FullText}},
		})
	}
	for _, index := range sortedIntKeys(state.ToolBuffers) {
		buf := state.ToolBuffers[index]
		itemID := state.ToolItemIDs[index]
		if itemID == "" {
			itemID = "fc_" + util.RandomID(12)
		}
		callID := buf["id"]
		if callID == "" {
			callID = util.CallID()
		}
		output = append(output, map[string]any{
			"id": itemID, "type": "function_call", "status": "completed",
			"call_id": callID, "name": buf["name"], "arguments": buf["arguments"],
		})
	}
	response := state.baseResponse("completed")
	response["output"] = output
	if usage != nil {
		resolved := util.ResolveUsage(usage, promptText, completionText)
		response["usage"] = convert.MapResponsesUsage(resolved)
	}
	return []string{EmitEvent("response.completed", map[string]any{
		"response": response, "sequence_number": state.nextSeq(),
	})}
}

// StreamResponsesFromOpenAIChat converts upstream chat SSE into Responses
// events. Port of stream_responses_from_openai_chat.
func StreamResponsesFromOpenAIChat(source util.LineSource, emit Emit, model string, tools []models.ToolDef, protocol string, stripThink, promptFC bool, legacyMap map[string]string, promptText string) error {
	if legacyMap == nil {
		legacyMap = map[string]string{}
	}
	state := NewResponsesStreamState(model)
	for _, frame := range StartEvents(state) {
		if err := emit(frame); err != nil {
			return err
		}
	}
	var sieve *engine.ToolSieve
	if promptFC {
		sieve = fc.CreateSieve(tools, protocol, 96)
	}
	var fullPromptText []string
	var completionParts []string
	usage := map[string]any{}
	sawDone := false

	emitAll := func(frames []string) error {
		for _, frame := range frames {
			if err := emit(frame); err != nil {
				return err
			}
		}
		return nil
	}
	emitPromptCalls := func(callsRaw []engine.ParsedToolCall) error {
		if err := emitAll(FinishTextItem(state)); err != nil {
			return err
		}
		for i, item := range callsRaw {
			tc := fc.ParsedToToolCall(item)
			argsString := jsonx.Dumps(tc.Arguments)
			callID := tc.ID
			if callID == "" {
				callID = util.CallID()
			}
			if err := emitAll(ToolArgsDelta(state, i, callID, tc.Name, argsString)); err != nil {
				return err
			}
		}
		if err := emitAll(FinishToolItems(state)); err != nil {
			return err
		}
		return emitAll(CompleteEvents(state, usage, "", promptText, strings.Join(fullPromptText, "")))
	}

	it := newEventIterator(source)
	for {
		event, ok, err := it.Next()
		if err != nil {
			return emitAll(CompleteEvents(state, nil, err.Error(), "", ""))
		}
		if !ok {
			break
		}
		if event.IsDone() {
			sawDone = true
			break
		}
		payload := event.JSON()
		if payload == nil {
			continue
		}
		if u, ok := payload["usage"].(map[string]any); ok {
			usage = u
		}
		choices := jsonx.AsList(payload["choices"])
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]any)
		delta, _ := choice["delta"].(map[string]any)
		finish := choice["finish_reason"]

		if promptFC {
			if delta == nil {
				continue
			}
			if piece := delta["content"]; !jsonx.Falsy(piece) {
				text := jsonx.Str(piece)
				fullPromptText = append(fullPromptText, text)
				for _, sev := range sieve.ProcessChunk(text) {
					switch sev.Type {
					case "content":
						if err := emitAll(TextDeltaEvents(state, sev.Text)); err != nil {
							return err
						}
					case "tool_calls":
						return emitPromptCalls(sev.Calls)
					}
				}
			}
			continue
		}

		// Native path
		if delta != nil {
			if piece := delta["content"]; !jsonx.Falsy(piece) {
				completionParts = append(completionParts, jsonx.Str(piece))
				if err := emitAll(TextDeltaEvents(state, jsonx.Str(piece))); err != nil {
					return err
				}
			}
			for _, rawTC := range jsonx.AsList(delta["tool_calls"]) {
				tc, ok := rawTC.(map[string]any)
				if !ok {
					continue
				}
				index := jsonx.IntValue(tc["index"])
				fn, _ := tc["function"].(map[string]any)
				rawName := ""
				argsDelta := ""
				if fn != nil {
					rawName = jsonx.Str(fn["name"])
					argsDelta = jsonx.Str(fn["arguments"])
				}
				name := engine.CanonicalizeResponseToolName(rawName, legacyMap)
				if err := emitAll(ToolArgsDelta(state, index, jsonx.Str(tc["id"]), name, argsDelta)); err != nil {
					return err
				}
			}
		}
		if finish == "tool_calls" {
			if err := emitAll(FinishTextItem(state)); err != nil {
				return err
			}
			if err := emitAll(FinishToolItems(state)); err != nil {
				return err
			}
			return emitAll(CompleteEvents(state, usage, "", promptText, strings.Join(completionParts, "")))
		}
		if finish == "stop" || finish == "length" || finish == "content_filter" {
			break
		}
	}

	if promptFC && sieve != nil {
		for _, sev := range sieve.Flush() {
			switch sev.Type {
			case "content":
				if err := emitAll(TextDeltaEvents(state, sev.Text)); err != nil {
					return err
				}
			case "tool_calls":
				if err := emitAll(FinishTextItem(state)); err != nil {
					return err
				}
				for i, item := range sev.Calls {
					tc := fc.ParsedToToolCall(item)
					callID := tc.ID
					if callID == "" {
						callID = util.CallID()
					}
					if err := emitAll(ToolArgsDelta(state, i, callID, tc.Name, jsonx.Dumps(tc.Arguments))); err != nil {
						return err
					}
				}
			}
		}
		if len(state.ToolBuffers) == 0 {
			full := strings.Join(fullPromptText, "")
			calls, err := fc.ParseTextToCalls(full, tools, protocol, stripThink)
			if err != nil {
				return emitAll(CompleteEvents(state, nil, err.Error(), "", ""))
			}
			for i, call := range calls {
				callID := call.ID
				if callID == "" {
					callID = util.CallID()
				}
				if err := emitAll(ToolArgsDelta(state, i, callID, call.Name, jsonx.Dumps(call.Arguments))); err != nil {
					return err
				}
			}
		}
	}
	if len(state.ToolBuffers) > 0 {
		if err := emitAll(FinishTextItem(state)); err != nil {
			return err
		}
		if err := emitAll(FinishToolItems(state)); err != nil {
			return err
		}
	} else {
		if err := emitAll(FinishTextItem(state)); err != nil {
			return err
		}
	}
	completionText := strings.Join(completionParts, "")
	if promptFC {
		completionText = strings.Join(fullPromptText, "")
	}
	_ = sawDone
	return emitAll(CompleteEvents(state, usage, "", promptText, completionText))
}
