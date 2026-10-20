package engine

import (
	"fmt"
	"strings"

	"github.com/Sliverkiss/toolkit/jsonx"
)

// ParseToolCalls extracts normalized calls from markup, JSON, XML or text
// key/value output. Port of parse_tool_calls. Config-driven validation errors
// that Python raises as ValueError are returned as errors.
func ParseToolCalls(text any, tools any, config *ToolCallConfig) ([]ParsedToolCall, error) {
	if config == nil {
		config = DefaultConfig()
	}
	normalizedTools := NormalizeTools(tools)
	if strings.TrimSpace(strValue(text)) == "" || len(normalizedTools) == 0 {
		return nil, nil
	}
	allowed := buildAllowedToolMap(normalizedTools, config)
	var calls []ParsedToolCall
	if config.EnableMarkup {
		for _, protocol := range config.ParseProtocols {
			calls = append(calls, parseProtocolMarkup(text, protocol, allowed, normalizedTools, config)...)
		}
	}
	if config.EnableXML {
		calls = append(calls, parseXMLToolCalls(text, allowed, config)...)
	}
	if config.EnableJSON {
		visit := func(value any) {
			calls = append(calls, parseJSONToolCalls(value, allowed, config)...)
		}
		forEachJSONFragment(text, visit)
	}
	if config.EnableTextKV {
		calls = append(calls, parseTextKVToolsCalls(text, allowed, normalizedTools, config)...)
	}
	if !config.EnableCoercion {
		if config.EnableDedupe {
			return dedupeToolCalls(calls), nil
		}
		return calls, nil
	}
	fixed := make([]ParsedToolCall, 0, len(calls))
	for _, call := range calls {
		parsed, err := coerceParsedCall(call, normalizedTools, config)
		if err != nil {
			return nil, err
		}
		if parsed != nil {
			fixed = append(fixed, *parsed)
		}
	}
	if config.EnableDedupe {
		return dedupeToolCalls(fixed), nil
	}
	return fixed, nil
}

func parseProtocolMarkup(text any, protocol ProtocolSpec, allowed map[string]string, tools []map[string]any, config *ToolCallConfig) []ParsedToolCall {
	canonical := canonicalizeMarkup(stripMarkdownFences(strValue(text)))
	var calls []ParsedToolCall
	toolNames := map[string]struct{}{}
	for _, tool := range NormalizeTools(tools) {
		if name, ok := tool["name"].(string); ok && name != "" {
			toolNames[name] = struct{}{}
		}
	}
	soleTool := ""
	if len(toolNames) == 1 {
		for name := range toolNames {
			soleTool = name
		}
	}
	for _, candidate := range extractProtocolCandidates(canonical, protocol) {
		for _, blockTag := range invokeBlockTags(protocol) {
			for _, block := range iterProtocolBlocks(candidate, protocol, blockTag) {
				name := canonicalToolName(extractNameAttr(block.Attrs), allowed, config)
				if name == "" {
					name = soleTool
					if name == "" {
						continue
					}
				}
				input := parseProtocolParameters(block.Body, protocol, config)
				calls = append(calls, ParsedToolCall{ID: config.IDFactory(), Name: name, Input: input})
			}
		}
	}
	if len(calls) == 0 {
		calls = append(calls, parseToolNameTagCalls(canonical, protocol, allowed, tools, config)...)
	}
	if len(calls) == 0 {
		calls = append(calls, parseLooseProtocolCalls(canonical, protocol, allowed, tools, config)...)
	}
	if len(calls) == 0 {
		calls = append(calls, parseRootBodyImplicitInvoke(canonical, protocol, allowed, tools, config, soleTool)...)
	}
	if len(calls) == 0 {
		calls = append(calls, parseOrphanParameterCalls(canonical, protocol, allowed, tools, config, soleTool)...)
	}
	if len(calls) == 0 {
		calls = append(calls, parseCollapsedProtocolCalls(canonical, protocol, allowed, tools, config, soleTool)...)
	}
	return calls
}

// parseToolNameTagCalls is the R22 recovery: the model used the tool name as the
// invoke tag name.
func parseToolNameTagCalls(text string, protocol ProtocolSpec, allowed map[string]string, tools []map[string]any, config *ToolCallConfig) []ParsedToolCall {
	openRe := protocolAnyOpenTagRe(protocol.Root, protocol.Invoke, protocol.Parameter)
	closeRe := protocolAnyCloseTagRe(protocol.Root, protocol.Invoke, protocol.Parameter)
	structural := map[string]struct{}{
		strings.ToLower(protocol.Root):      {},
		strings.ToLower(protocol.Invoke):    {},
		strings.ToLower(protocol.Parameter): {},
		"tool":                              {},
	}
	type event struct {
		kind  string
		start int
		end   int
		tag   string
	}
	var events []event
	for _, match := range openRe.FindAllStringSubmatchIndex(text, -1) {
		start, end := match[0], match[1]
		tag := submatch(text, match, openRe.SubexpIndex("tag"))
		events = append(events, event{kind: "open", start: start, end: end, tag: tag})
	}
	for _, match := range closeRe.FindAllStringIndex(text, -1) {
		events = append(events, event{kind: "close", start: match[0], end: match[1]})
	}
	// Stable sort by start offset (Python's list.sort is stable).
	for i := 1; i < len(events); i++ {
		for j := i; j > 0 && events[j].start < events[j-1].start; j-- {
			events[j], events[j-1] = events[j-1], events[j]
		}
	}
	type span struct {
		start, end int
		name       string
	}
	type stackItem struct {
		tag        string
		start, end int
	}
	var spans []span
	var stack []stackItem
	nonStructural := func(tag string) bool {
		_, ok := structural[strings.ToLower(tag)]
		return !ok
	}
	hasNonStructural := func() bool {
		for _, item := range stack {
			if nonStructural(item.tag) {
				return true
			}
		}
		return false
	}
	for _, ev := range events {
		if ev.kind == "open" {
			if strings.EqualFold(ev.tag, protocol.Invoke) && hasNonStructural() {
				for len(stack) > 0 {
					popped := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					if nonStructural(popped.tag) {
						spans = append(spans, span{start: popped.end, end: ev.start, name: popped.tag})
						break
					}
				}
			} else {
				stack = append(stack, stackItem{tag: ev.tag, start: ev.start, end: ev.end})
			}
		} else if len(stack) > 0 {
			popped := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if nonStructural(popped.tag) {
				spans = append(spans, span{start: popped.end, end: ev.start, name: popped.tag})
			}
		}
	}
	var calls []ParsedToolCall
	for _, sp := range spans {
		toolName := canonicalToolName(sp.name, allowed, config)
		if toolName == "" {
			continue
		}
		body := text[sp.start:sp.end]
		params := parseProtocolParameters(body, protocol, config)
		if len(params) > 0 {
			calls = append(calls, ParsedToolCall{ID: config.IDFactory(), Name: toolName, Input: params})
		}
	}
	return calls
}

func invokeBlockTags(protocol ProtocolSpec) []string {
	tags := []string{protocol.Invoke}
	if protocol.Invoke != "tool" {
		tags = append(tags, "tool")
	}
	return tags
}

func extractProtocolCandidates(text string, protocol ProtocolSpec) []string {
	var candidates []string
	for _, block := range iterProtocolBlocks(text, protocol, protocol.Root) {
		candidates = append(candidates, block.Body)
	}
	if len(candidates) > 0 {
		return candidates
	}
	if loc := protocolOpenTagRe(protocol.Invoke).FindStringIndex(text); loc != nil {
		return []string{text[loc[0]:]}
	}
	return nil
}

func parseProtocolParameters(body string, protocol ProtocolSpec, config *ToolCallConfig) map[string]any {
	out := map[string]any{}
	for _, block := range iterProtocolBlocks(body, protocol, protocol.Parameter) {
		name := extractNameAttr(block.Attrs)
		if name != "" {
			out[strings.ToLower(name)] = decodeMarkupValue(block.Body, name, config)
		}
	}
	recoverParameterNameTags(out, body, protocol, config)
	if len(out) > 0 {
		return out
	}
	return parseTextKVInput(body)
}

func recoverParameterNameTags(out map[string]any, body string, protocol ProtocolSpec, config *ToolCallConfig) {
	if len(out) > 0 {
		return
	}
	structural := map[string]struct{}{"parameter": {}, "invoke": {}, "tool_calls": {}, "root": {}, "tool": {}}
	for _, match := range findParameterNameTags(body, protocol.Name) {
		if _, ok := structural[strings.ToLower(match.Name)]; ok {
			continue
		}
		out[strings.ToLower(match.Name)] = decodeMarkupValue(match.Val, match.Name, config)
	}
}

func parseRootBodyImplicitInvoke(text string, protocol ProtocolSpec, allowed map[string]string, tools []map[string]any, config *ToolCallConfig, soleTool string) []ParsedToolCall {
	if soleTool == "" {
		return nil
	}
	var candidates []string
	for _, block := range iterProtocolBlocks(text, protocol, protocol.Root) {
		candidates = append(candidates, block.Body)
	}
	if len(candidates) == 0 {
		return nil
	}
	var calls []ParsedToolCall
	for _, body := range candidates {
		if protocolOpenTagRe(protocol.Invoke).MatchString(body) {
			continue
		}
		params := parseProtocolParameters(body, protocol, config)
		if len(params) == 0 {
			continue
		}
		calls = append(calls, ParsedToolCall{ID: config.IDFactory(), Name: soleTool, Input: params})
	}
	return calls
}

func parseOrphanParameterCalls(text string, protocol ProtocolSpec, allowed map[string]string, tools []map[string]any, config *ToolCallConfig, soleTool string) []ParsedToolCall {
	if soleTool == "" {
		return nil
	}
	for _, tag := range []string{protocol.Root, protocol.Invoke} {
		if protocolOpenTagRe(tag).MatchString(text) {
			return nil
		}
	}
	if !protocolOpenTagRe(protocol.Parameter).MatchString(text) {
		return nil
	}
	blocks := iterProtocolBlocks(text, protocol, protocol.Parameter)
	if len(blocks) == 0 {
		return nil
	}
	params := map[string]any{}
	for _, block := range blocks {
		name := extractNameAttr(block.Attrs)
		if name == "" {
			continue
		}
		params[strings.ToLower(name)] = decodeMarkupValue(block.Body, name, config)
	}
	if len(params) == 0 {
		return nil
	}
	properties := schemaProperties(toolSchema(soleTool, tools))
	if len(properties) == 0 {
		return nil
	}
	accepted := filterInputForTool(soleTool, params, tools)
	if len(accepted) == 0 {
		return nil
	}
	return []ParsedToolCall{{ID: config.IDFactory(), Name: soleTool, Input: accepted}}
}

func parseCollapsedProtocolCalls(text string, protocol ProtocolSpec, allowed map[string]string, tools []map[string]any, config *ToolCallConfig, soleTool string) []ParsedToolCall {
	if soleTool == "" {
		return nil
	}
	params := map[string]any{}
	for _, match := range findCollapsedParams(text) {
		if match.Name == "" {
			continue
		}
		key := strings.ToLower(match.Name)
		if _, ok := params[key]; ok {
			continue
		}
		params[key] = decodeMarkupValue(match.Value, match.Name, config)
	}
	if len(params) == 0 {
		return nil
	}
	properties := schemaProperties(toolSchema(soleTool, tools))
	if len(properties) == 0 {
		return nil
	}
	accepted := filterInputForTool(soleTool, params, tools)
	if len(accepted) == 0 {
		return nil
	}
	return []ParsedToolCall{{ID: config.IDFactory(), Name: soleTool, Input: accepted}}
}

func parseLooseProtocolCalls(text string, protocol ProtocolSpec, allowed map[string]string, tools []map[string]any, config *ToolCallConfig) []ParsedToolCall {
	if !regexpContainsWord(text, protocol.Name) {
		return nil
	}
	type attribute struct {
		raw      string
		name     string
		isTool   bool
		position int
	}
	sections := CDATASections(text)
	insideCDATA := func(pos int) bool {
		for _, section := range sections {
			if section[0] <= pos && pos < section[1] {
				return true
			}
		}
		return false
	}
	var attributes []attribute
	for _, match := range attrRe.FindAllStringSubmatchIndex(text, -1) {
		// CDATA content is opaque (R24/R33): a tool-name attribute that appears
		// inside an outer CDATA literal belongs to the literal, not the envelope.
		if insideCDATA(match[0]) {
			continue
		}
		group := ""
		for i := 2; i+1 < len(match); i += 2 {
			if match[i] >= 0 {
				group = text[match[i]:match[i+1]]
				break
			}
		}
		raw := htmlUnescape(strings.TrimSpace(group))
		if raw == "" {
			continue
		}
		name := canonicalToolName(raw, allowed, config)
		display := name
		if display == "" {
			display = raw
		}
		attributes = append(attributes, attribute{raw: raw, name: display, isTool: name != "", position: match[0]})
	}
	var calls []ParsedToolCall
	for index, item := range attributes {
		if !item.isTool {
			continue
		}
		nextTool := len(text)
		for _, later := range attributes[index+1:] {
			if later.isTool {
				nextTool = later.position
				break
			}
		}
		input := map[string]any{}
		for _, field := range attributes[index+1:] {
			if field.position >= nextTool || field.isTool {
				break
			}
			cdataMatches := ExtractCDATAFromValue(text[field.position:nextTool])
			if len(cdataMatches) > 0 {
				_, rawString := config.RawStringParams[strings.ToLower(field.raw)]
				input[field.raw] = coerceMarkupScalar(cdataMatches[0], rawString)
			}
		}
		filtered := filterInputForTool(item.name, input, tools)
		if len(filtered) == 0 && len(requiredToolArgs(item.name, tools)) > 0 {
			continue
		}
		calls = append(calls, ParsedToolCall{ID: config.IDFactory(), Name: item.name, Input: filtered})
	}
	return calls
}

func parseXMLToolCalls(text any, allowed map[string]string, config *ToolCallConfig) []ParsedToolCall {
	var calls []ParsedToolCall
	rawText := strValue(text)
	for _, re := range xmlToolCallRes {
		for _, match := range re.FindAllStringSubmatch(rawText, -1) {
			attrs := match[1]
			body := strings.TrimSpace(match[2])
			name := canonicalToolName(extractNameAttr(attrs), allowed, config)
			if name != "" {
				calls = append(calls, ParsedToolCall{
					ID: config.IDFactory(), Name: name, Input: parseToolInput(body),
				})
				continue
			}
			if ok, parsed := tryJSON(body); ok {
				calls = append(calls, parseJSONToolCalls(parsed, allowed, config)...)
			}
		}
	}
	return calls
}

func parseJSONToolCalls(value any, allowed map[string]string, config *ToolCallConfig) []ParsedToolCall {
	var calls []ParsedToolCall
	if list, ok := value.([]any); ok {
		for _, item := range list {
			calls = append(calls, parseJSONToolCalls(item, allowed, config)...)
		}
		return calls
	}
	m, ok := value.(map[string]any)
	if !ok {
		return calls
	}
	for _, key := range []string{"tool_calls", "tools"} {
		if list, ok := m[key].([]any); ok {
			for _, item := range list {
				calls = append(calls, parseJSONToolCalls(item, allowed, config)...)
			}
		}
	}
	name := firstString(m["name"], m["tool"], m["tool_name"], m["function_name"])
	input := firstDefined(m["input"], m["arguments"], m["args"], m["parameters"])
	if fn, ok := m["function"].(map[string]any); ok {
		if name == "" {
			name = firstString(fn["name"])
		}
		if input == nil {
			input = firstDefined(fn["arguments"], fn["input"], fn["parameters"])
		}
	}
	canonicalName := canonicalToolName(name, allowed, config)
	if canonicalName == "" {
		return calls
	}
	id := firstString(m["id"], m["call_id"])
	if id == "" {
		id = config.IDFactory()
	}
	calls = append(calls, ParsedToolCall{
		ID: id, Name: canonicalName, Input: normalizeToolInput(input),
	})
	return calls
}

func parseTextKVToolsCalls(text any, allowed map[string]string, tools []map[string]any, config *ToolCallConfig) []ParsedToolCall {
	values := map[string][]string{"name": {}, "arguments": {}}
	current := ""
	aliases := map[string]string{
		"function.name": "name", "name": "name", "tool": "name", "tool.name": "name", "tool_name": "name",
		"function.arguments": "arguments", "arguments": "arguments", "args": "arguments",
		"input": "arguments", "tool_input": "arguments", "parameters": "arguments",
	}
	for _, rawLine := range splitLinesPython(strValue(text)) {
		line := strings.TrimSpace(rawLine)
		if match := textKVLineRe.FindStringSubmatch(line); match != nil {
			if mapped, ok := aliases[strings.ToLower(match[1])]; ok {
				current = mapped
				values[current] = append(values[current], strings.TrimSpace(match[2]))
				continue
			}
		}
		if current != "" {
			values[current] = append(values[current], rawLine)
		}
	}
	if len(values["name"]) == 0 {
		return nil
	}
	rawName := strings.TrimSpace(strings.SplitN(strings.Join(values["name"], "\n"), "\n", 2)[0])
	rawName = strings.Trim(rawName, `'"`)
	name := canonicalToolName(rawName, allowed, config)
	if name == "" {
		return nil
	}
	input := normalizeToolInput(strings.TrimSpace(strings.Join(values["arguments"], "\n")))
	return []ParsedToolCall{{ID: config.IDFactory(), Name: name, Input: input}}
}

func coerceParsedCall(call ParsedToolCall, tools []map[string]any, config *ToolCallConfig) (*ParsedToolCall, error) {
	input := CoerceToolInput(call.Name, call.Input, tools, config)
	if m, ok := input.(map[string]any); ok {
		input = filterInputForTool(call.Name, m, tools)
	}
	if config.UnknownTool == "error" && toolSchema(call.Name, tools) == nil {
		return nil, fmt.Errorf("Unknown tool: %s", call.Name)
	}
	if missingRequiredArgs(call.Name, input, tools) {
		if config.MissingRequired == "error" || config.Strict {
			return nil, fmt.Errorf("Missing required arguments for tool: %s", call.Name)
		}
		if config.MissingRequired == "drop" {
			return nil, nil
		}
	}
	if invalidToolArgs(input) {
		return nil, nil
	}
	return &ParsedToolCall{ID: call.ID, Name: call.Name, Input: input}, nil
}

// CoerceToolInput applies schema coercion and common aliases. Port of
// coerce_tool_input.
func CoerceToolInput(name string, input any, tools any, config *ToolCallConfig) any {
	if config == nil {
		config = DefaultConfig()
	}
	normalizedTools := NormalizeTools(tools)
	fixed := coerceToolInputBySchema(name, input, normalizedTools)
	m, ok := fixed.(map[string]any)
	if !ok {
		return fixed
	}
	copied := make(map[string]any, len(m))
	for key, value := range m {
		copied[key] = value
	}
	fixed = copied
	if aliases, ok := config.ArgumentAliases[name]; ok {
		for canonical, alternates := range aliases {
			renameFirstPresent(fixed.(map[string]any), canonical, alternates...)
		}
	}
	obj := fixed.(map[string]any)
	switch name {
	case "AskUserQuestion":
		if obj["question"] != nil && obj["questions"] == nil {
			obj["questions"] = []any{map[string]any{
				"question":    obj["question"],
				"header":      "Question",
				"multiSelect": false,
				"options": []any{
					map[string]any{"label": "Yes", "description": "Confirm"},
					map[string]any{"label": "No", "description": "Decline"},
				},
			}}
			delete(obj, "question")
		}
		if obj["questions"] != nil {
			if _, ok := obj["questions"].([]any); !ok {
				obj["questions"] = []any{obj["questions"]}
			}
		}
	case "Agent":
		if obj["description"] == nil {
			obj["description"] = "Execute sub-task"
		}
		if obj["prompt"] == nil {
			obj["prompt"] = obj["description"]
		}
	case "Read":
		renameFirstPresent(obj, "file_path", "path", "filename", "file")
	case "Write":
		renameFirstPresent(obj, "file_path", "path", "target_file", "filename", "file")
		renameFirstPresent(obj, "content", "text", "body", "data", "file_content", "contents", "value")
	case "Edit":
		renameFirstPresent(obj, "file_path", "path", "target_file", "filename", "file")
	case "Bash", "PowerShell":
		renameFirstPresent(obj, "command", "cmd", "script")
	default:
		if obj["query"] == nil && obj["queries"] != nil && toolAcceptsField(name, normalizedTools, "query") {
			queries := obj["queries"]
			delete(obj, "queries")
			if list, ok := queries.([]any); ok {
				parts := make([]string, 0, len(list))
				for _, item := range list {
					if s := jsonx.Str(item); s != "" {
						parts = append(parts, s)
					}
				}
				obj["query"] = strings.Join(parts, "\n")
			} else {
				obj["query"] = strings.TrimSpace(jsonx.Str(queries))
			}
		}
	}
	return fixed
}

func coerceToolInputBySchema(name string, input any, tools []map[string]any) any {
	m, ok := input.(map[string]any)
	if !ok {
		return input
	}
	properties := schemaProperties(toolSchema(name, tools))
	if len(properties) == 0 {
		return input
	}
	fixed := make(map[string]any, len(m))
	for key, value := range m {
		fixed[key] = value
	}
	for key, value := range fixed {
		if child, ok := properties[key].(map[string]any); ok {
			fixed[key] = coerceValueBySchema(value, child)
		}
	}
	return fixed
}

func coerceValueBySchema(value any, schema map[string]any) any {
	types := schemaTypes(schema)
	if s, ok := value.(string); ok {
		_, wantArray := types["array"]
		_, wantObject := types["object"]
		if wantArray || wantObject {
			if parsed, changed := parseJSONStringForSchema(s, wantArray, wantObject); changed {
				value = parsed
			}
		}
	}
	if _, ok := types["array"]; ok {
		if m, ok := value.(map[string]any); ok {
			value = []any{m}
		}
		if list, ok := value.([]any); ok {
			if items, ok := schema["items"].(map[string]any); ok {
				out := make([]any, len(list))
				for i, item := range list {
					out[i] = coerceValueBySchema(item, items)
				}
				return out
			}
		}
		return value
	}
	if _, ok := types["object"]; ok {
		if m, ok := value.(map[string]any); ok {
			properties := schemaProperties(schema)
			if len(properties) == 0 {
				return value
			}
			fixed := make(map[string]any, len(m))
			for key, item := range m {
				fixed[key] = item
			}
			for key, child := range properties {
				childMap, ok := child.(map[string]any)
				if !ok {
					continue
				}
				if _, exists := fixed[key]; exists {
					fixed[key] = coerceValueBySchema(fixed[key], childMap)
				}
			}
			return fixed
		}
	}
	return value
}

func parseJSONStringForSchema(value string, wantArray, wantObject bool) (any, bool) {
	stripped := strings.TrimSpace(value)
	if stripped == "" {
		return value, false
	}
	candidates := []string{stripped}
	if wantArray && !strings.HasPrefix(stripped, "[") {
		candidates = append(candidates, "["+stripped+"]")
	}
	for _, candidate := range candidates {
		ok, parsed := tryJSON(candidate)
		if !ok {
			continue
		}
		if wantArray {
			if list, ok := parsed.([]any); ok {
				return list, true
			}
			if m, ok := parsed.(map[string]any); ok {
				return []any{m}, true
			}
		}
		if wantObject {
			if m, ok := parsed.(map[string]any); ok {
				return m, true
			}
		}
	}
	return value, false
}

func normalizeToolInput(value any) any {
	if value == nil {
		return map[string]any{}
	}
	if s, ok := value.(string); ok {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			return map[string]any{}
		}
		if ok, parsed := tryJSON(trimmed); ok {
			return normalizeToolInput(parsed)
		}
		if keyValues := parseTextKVInput(trimmed); len(keyValues) > 0 {
			return keyValues
		}
		return value
	}
	return value
}

func parseToolInput(text string) any {
	if text == "" {
		return map[string]any{}
	}
	if ok, parsed := tryJSON(text); ok {
		return normalizeToolInput(parsed)
	}
	parameters := map[string]any{}
	for _, match := range findXMLTagBodies(text) {
		parameters[match.Name] = decodeMarkupValue(match.Body, match.Name, DefaultConfig())
	}
	if len(parameters) > 0 {
		return parameters
	}
	if keyValues := parseTextKVInput(text); len(keyValues) > 0 {
		return keyValues
	}
	return map[string]any{"input": text}
}

func parseTextKVInput(text any) map[string]any {
	out := map[string]any{}
	for _, rawLine := range splitLinesPython(strValue(text)) {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		equals := strings.Index(line, "=")
		colon := strings.Index(line, ":")
		separator := equals
		if equals < 0 || (colon >= 0 && colon < equals) {
			separator = colon
		}
		if separator <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:separator])
		value := strings.Trim(strings.TrimSpace(line[separator+1:]), `'"`)
		if key != "" {
			out[key] = value
		}
	}
	return out
}

func forEachJSONFragment(text any, visit func(any)) {
	normalized := stripJSONFence(strValue(text))
	for _, candidate := range []string{normalized, repairLooseJSON(normalized), recoverJSONLike(normalized)} {
		if ok, parsed := tryJSON(candidate); ok {
			visit(parsed)
		}
	}
	var starts []int
	for i := 0; i < len(normalized); i++ {
		if normalized[i] == '{' {
			starts = append(starts, i)
		}
	}
	for i := 0; i < len(normalized); i++ {
		if normalized[i] == '[' {
			starts = append(starts, i)
		}
	}
	for _, start := range starts {
		for end := len(normalized); end > start; end-- {
			fragment := normalized[start:end]
			if ok, parsed := tryJSON(fragment); ok {
				visit(parsed)
				break
			}
			if ok, parsed := tryJSON(repairLooseJSON(fragment)); ok {
				visit(parsed)
				break
			}
		}
	}
}

func dedupeToolCalls(calls []ParsedToolCall) []ParsedToolCall {
	seen := map[string]struct{}{}
	out := make([]ParsedToolCall, 0, len(calls))
	for _, call := range calls {
		key := toolAliasKey(call.Name) + "\x00" + stableStringify(call.Input)
		if call.Name == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, call)
	}
	return out
}

// Block is one protocol block: attribute text and inner body.
type Block struct {
	Attrs string
	Tag   string
	Body  string
}

// IterProtocolBlocks yields (attrs, body) for each top-level block of tag. Port
// of _iter_protocol_blocks.
func IterProtocolBlocks(text string, protocol ProtocolSpec, tag string) []Block {
	return iterProtocolBlocks(text, protocol, tag)
}

func iterProtocolBlocks(text string, protocol ProtocolSpec, tag string) []Block {
	cdataSections := CDATASections(text)
	insideCDATA := func(pos int) bool {
		for _, section := range cdataSections {
			if section[0] <= pos && pos < section[1] {
				return true
			}
		}
		return false
	}
	type event struct {
		kind       string
		start, end int
		attrs      string
		tag        string
		depth      int
	}
	var events []event
	openRe := protocolAnyOpenTagRe(protocol.Root, protocol.Invoke, protocol.Parameter)
	for _, match := range openRe.FindAllStringSubmatchIndex(text, -1) {
		if insideCDATA(match[0]) {
			continue
		}
		events = append(events, event{
			kind:  "open",
			start: match[0],
			end:   match[1],
			attrs: submatch(text, match, openRe.SubexpIndex("attrs")),
			tag:   submatch(text, match, openRe.SubexpIndex("tag")),
		})
	}
	closeRe := protocolAnyCloseTagRe(protocol.Root, protocol.Invoke, protocol.Parameter)
	for _, match := range closeRe.FindAllStringIndex(text, -1) {
		if insideCDATA(match[0]) {
			continue
		}
		events = append(events, event{kind: "close", start: match[0], end: match[1]})
	}
	for i := 1; i < len(events); i++ {
		for j := i; j > 0 && events[j].start < events[j-1].start; j-- {
			events[j], events[j-1] = events[j-1], events[j]
		}
	}
	var stack []event
	type pairing struct {
		opener event
		close  int
	}
	var pairings []pairing
	for _, ev := range events {
		if ev.kind == "open" {
			depth := 0
			for _, item := range stack {
				if strings.EqualFold(item.tag, ev.tag) {
					depth++
				}
			}
			ev.depth = depth
			stack = append(stack, ev)
		} else if len(stack) > 0 {
			opener := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			pairings = append(pairings, pairing{opener: opener, close: ev.start})
		}
	}
	wanted := strings.ToLower(tag)
	var out []Block
	for _, pair := range pairings {
		if strings.ToLower(pair.opener.tag) != wanted {
			continue
		}
		if pair.opener.depth != 0 {
			continue
		}
		out = append(out, Block{
			Attrs: pair.opener.attrs,
			Tag:   pair.opener.tag,
			Body:  text[pair.opener.end:pair.close],
		})
	}
	return out
}

func extractNameAttr(attributes any) string {
	raw := strings.ReplaceAll(strValue(attributes), "NAME", "name")
	match := nameAttrRe.FindStringSubmatch(raw)
	if match == nil {
		return ""
	}
	for _, group := range match[1:] {
		if group != "" {
			return htmlUnescape(strings.TrimSpace(group))
		}
	}
	return ""
}

// DecodeMarkupValue decodes one parameter value. Port of _decode_markup_value.
func DecodeMarkupValue(raw any, parameterName any, config *ToolCallConfig) any {
	if config == nil {
		config = DefaultConfig()
	}
	rawStr := strValue(raw)
	cdataMatches := ExtractCDATAFromValue(rawStr)
	_, rawString := config.RawStringParams[strings.ToLower(strValue(parameterName))]
	if len(cdataMatches) > 0 {
		joined := strings.Join(cdataMatches, "")
		if peeled, ok := stripNestedCDATAMarkers(joined, false); ok {
			joined = peeled
		}
		if rawString {
			return joined
		}
		return coerceMarkupScalar(joined, false)
	}
	if !rawString {
		if parsed, nested := parseNestedMarkupValue(rawStr, config); parsed {
			return nested
		}
	}
	collapsed := findCollapsedParams(rawStr)
	if len(collapsed) > 0 {
		first := collapsed[0]
		inner := rawStr[:first.Start] + first.Value + rawStr[first.End:]
		if rawString {
			return inner
		}
		return coerceMarkupScalar(inner, false)
	}
	if peeled, ok := stripNestedCDATAMarkers(rawStr, true); ok {
		if rawString {
			return peeled
		}
		return coerceMarkupScalar(peeled, false)
	}
	return coerceMarkupScalar(raw, rawString)
}

func parseNestedMarkupValue(raw string, config *ToolCallConfig) (bool, any) {
	text := strings.TrimSpace(raw)
	if text == "" || !strings.Contains(text, "<") {
		return false, nil
	}
	matches := findXMLTagBodies(text)
	if len(matches) == 0 {
		return false, nil
	}
	names := make([]string, len(matches))
	values := make([]any, len(matches))
	allItems := true
	for i, match := range matches {
		names[i] = match.Name
		values[i] = DecodeMarkupValue(match.Body, match.Name, config)
		if !strings.EqualFold(match.Name, "item") {
			allItems = false
		}
	}
	if allItems {
		return true, values
	}
	out := map[string]any{}
	for i, name := range names {
		value := values[i]
		existing, ok := out[name]
		if !ok {
			out[name] = value
			continue
		}
		if list, ok := existing.([]any); ok {
			out[name] = append(list, value)
		} else {
			out[name] = []any{existing, value}
		}
	}
	return true, out
}

func coerceMarkupScalar(raw any, rawString bool) any {
	value := htmlUnescape(strings.TrimSpace(strValue(raw)))
	if rawString {
		return value
	}
	switch strings.ToLower(value) {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if ok, parsed := tryJSON(value); ok {
		return normalizeToolInput(parsed)
	}
	return value
}

// decodeMarkupValue is the internal name used throughout the parser.
func decodeMarkupValue(raw any, parameterName any, config *ToolCallConfig) any {
	return DecodeMarkupValue(raw, parameterName, config)
}

func canonicalizeMarkup(text string) string {
	for _, pair := range MarkupReplacements {
		text = strings.ReplaceAll(text, pair[0], pair[1])
	}
	text = fusedTagRe.ReplaceAllStringFunc(text, func(match string) string {
		groups := fusedTagRe.FindStringSubmatch(match)
		if groups == nil {
			return match
		}
		return groups[1] + "|" + groups[2] + "|" + groups[4] + groups[5]
	})
	replacer := strings.NewReplacer(
		"\u200b", "", "\u200c", "", "\u200d", "", "\ufeff", "",
		"\u3000", " ", "\u00a0", " ",
	)
	return replacer.Replace(text)
}

func stripMarkdownFences(text string) string { return mdFenceRe.ReplaceAllString(text, "$1") }

func stripJSONFence(text string) string {
	trimmed := strings.TrimSpace(text)
	if loc := jsonFenceRe.FindStringSubmatchIndex(trimmed); loc != nil && loc[0] == 0 && loc[1] == len(trimmed) {
		return strings.TrimSpace(trimmed[loc[2]:loc[3]])
	}
	return trimmed
}

func repairLooseJSON(text string) string {
	repaired := strings.TrimSpace(text)
	repaired = repairNameEqRe.ReplaceAllString(repaired, `"name": "`)
	repaired = repairNameEq2Re.ReplaceAllString(repaired, `"name": "$1"`)
	repaired = repairKeyEqRe.ReplaceAllString(repaired, `"$1": `)
	return repairKeyColonRe.ReplaceAllString(repaired, `$1"$2":`)
}

func recoverJSONLike(text string) string {
	repaired := strings.TrimSpace(text)
	unclosedBraces := strings.Count(repaired, "{") - strings.Count(repaired, "}")
	unclosedBrackets := strings.Count(repaired, "[") - strings.Count(repaired, "]")
	if unclosedBrackets > 0 {
		repaired += strings.Repeat("]", unclosedBrackets)
	}
	if unclosedBraces > 0 {
		repaired += strings.Repeat("}", unclosedBraces)
	}
	return repaired
}

func submatch(text string, match []int, index int) string {
	if index < 0 || index*2+1 >= len(match) {
		return ""
	}
	if match[index*2] < 0 {
		return ""
	}
	return text[match[index*2]:match[index*2+1]]
}

// splitLinesPython mirrors str.splitlines() for \n, \r\n and \r.
func splitLinesPython(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
			continue
		}
		if s[i] == '\r' {
			out = append(out, s[start:i])
			if i+1 < len(s) && s[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// regexpContainsWord implements Python's r"\bNAME\b" search.
func regexpContainsWord(text, word string) bool {
	lower := strings.ToLower(text)
	needle := strings.ToLower(word)
	from := 0
	for {
		idx := strings.Index(lower[from:], needle)
		if idx < 0 {
			return false
		}
		pos := from + idx
		beforeOK := pos == 0 || !isWordByte(lower[pos-1])
		after := pos + len(needle)
		afterOK := after >= len(lower) || !isWordByte(lower[after])
		if beforeOK && afterOK {
			return true
		}
		from = pos + 1
	}
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
