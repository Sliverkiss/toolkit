package engine

import (
	"strings"
	"sync"

	"github.com/Sliverkiss/toolkit/jsonx"
)

// normalizeToolsResult is the cached normalization output. Port of
// _normalized_tools (lru_cache in Python, sync.Map here).
var normalizedToolsCache sync.Map // key string -> []map[string]any

// NormalizeTools accepts OpenAI function specs and plain tool specs and returns
// fresh deep copies. Port of normalize_tools.
func NormalizeTools(value any) []map[string]any {
	items := jsonx.AsList(value)
	key := normalizeToolsKey(items)
	if cached, ok := normalizedToolsCache.Load(key); ok {
		return deepCopyTools(cached.([]map[string]any))
	}
	var out []map[string]any
	for _, raw := range items {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == "function" {
			if fn, ok := m["function"].(map[string]any); ok {
				out = append(out, deepCopyMap(fn))
				continue
			}
		}
		if name, ok := m["name"].(string); ok && strings.TrimSpace(name) != "" {
			out = append(out, deepCopyMap(m))
		}
	}
	if out == nil {
		out = []map[string]any{}
	}
	normalizedToolsCache.Store(key, deepCopyTools(out))
	return out
}

func normalizeToolsKey(items []any) string {
	parts := make([]string, 0, len(items))
	for _, raw := range items {
		parts = append(parts, canonicalJSON(raw))
	}
	return strings.Join(parts, "\x00")
}

// canonicalJSON is json.dumps(value, sort_keys=True, default=str).
func canonicalJSON(value any) string {
	return jsonx.Dumps(sortKeysDeep(value))
}

func sortKeysDeep(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			out[key] = sortKeysDeep(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = sortKeysDeep(child)
		}
		return out
	default:
		return value
	}
}

func deepCopyMap(src map[string]any) map[string]any {
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = deepCopyValue(value)
	}
	return out
}

func deepCopyValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return deepCopyMap(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = deepCopyValue(item)
		}
		return out
	default:
		return value
	}
}

func deepCopyTools(src []map[string]any) []map[string]any {
	out := make([]map[string]any, len(src))
	for i, tool := range src {
		out[i] = deepCopyMap(tool)
	}
	return out
}

type toolsKeyEntry struct {
	name   string
	params string
}

func toolsKey(tools any) []toolsKeyEntry {
	normalized := NormalizeTools(tools)
	entries := make([]toolsKeyEntry, 0, len(normalized))
	for _, tool := range normalized {
		name, _ := tool["name"].(string)
		if name == "" {
			continue
		}
		var params any
		if tool["parameters"] != nil {
			params = tool["parameters"]
		} else {
			params = tool["input_schema"]
		}
		entries = append(entries, toolsKeyEntry{name: name, params: canonicalJSON(params)})
	}
	// Python sorts (name, json) tuples.
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0; j-- {
			if entries[j].name < entries[j-1].name ||
				(entries[j].name == entries[j-1].name && entries[j].params < entries[j-1].params) {
				entries[j], entries[j-1] = entries[j-1], entries[j]
				break
			}
		}
	}
	return entries
}

var toolSchemaCache sync.Map // key string -> map[string]map[string]any

// toolSchema ports _tool_schema (content-keyed, bounded by process lifetime;
// Python used lru_cache(maxsize=64) after the id()-keyed bug fix).
func toolSchema(name string, tools any) map[string]any {
	entries := toolsKey(tools)
	keyParts := make([]string, 0, len(entries))
	for _, entry := range entries {
		keyParts = append(keyParts, entry.name+"\x00"+entry.params)
	}
	key := strings.Join(keyParts, "\x01")
	cached, ok := toolSchemaCache.Load(key)
	if !ok {
		schemas := make(map[string]map[string]any, len(entries))
		for _, entry := range entries {
			if decoded, ok := jsonx.TryJSONValue(entry.params); ok {
				if m, ok := decoded.(map[string]any); ok {
					schemas[entry.name] = m
				}
			}
		}
		cached, _ = toolSchemaCache.LoadOrStore(key, schemas)
	}
	schemas := cached.(map[string]map[string]any)
	return schemas[name]
}

// schemaProperties ports _schema_properties.
func schemaProperties(schema any) map[string]any {
	m, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	props, ok := m["properties"].(map[string]any)
	if !ok {
		return nil
	}
	return props
}

// schemaTypes ports _schema_types.
func schemaTypes(schema any) map[string]struct{} {
	types := map[string]struct{}{}
	m, ok := schema.(map[string]any)
	if !ok {
		return types
	}
	switch kind := m["type"].(type) {
	case string:
		types[kind] = struct{}{}
	case []any:
		for _, item := range kind {
			if s, ok := item.(string); ok {
				types[s] = struct{}{}
			}
		}
	}
	if m["properties"] != nil {
		types["object"] = struct{}{}
	}
	if m["items"] != nil {
		types["array"] = struct{}{}
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		if list, ok := m[key].([]any); ok {
			for _, variant := range list {
				for t := range schemaTypes(variant) {
					types[t] = struct{}{}
				}
			}
		}
	}
	return types
}

// requiredToolArgs ports _required_tool_args.
func requiredToolArgs(name string, tools any) []string {
	seen := map[string]struct{}{}
	var required []string
	add := func(keys ...string) {
		for _, key := range keys {
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			required = append(required, key)
		}
	}
	schema := toolSchema(name, tools)
	if schema != nil {
		if list, ok := schema["required"].([]any); ok {
			for _, item := range list {
				if key, ok := item.(string); ok {
					add(key)
				}
			}
		}
	}
	switch name {
	case "Read":
		add("file_path")
	case "Write":
		add("file_path", "content")
	case "Edit":
		add("file_path")
	case "Bash", "PowerShell":
		add("command")
	}
	return required
}

// missingRequiredArgs ports _missing_required_args.
func missingRequiredArgs(name string, input any, tools any) bool {
	m, ok := input.(map[string]any)
	if !ok {
		return false
	}
	for _, key := range requiredToolArgs(name, tools) {
		value, exists := m[key]
		if !exists || value == nil {
			return true
		}
		if s, ok := value.(string); ok && strings.TrimSpace(s) == "" && !requiredArgAllowsEmptyString(name, key) {
			return true
		}
	}
	return false
}

func requiredArgAllowsEmptyString(toolName, argumentName string) bool {
	tool := toolAliasKey(toolName)
	arg := toolAliasKey(argumentName)
	writeTools := map[string]struct{}{"write": {}, "writefile": {}, "createfile": {}}
	contentArgs := map[string]struct{}{
		"content": {}, "text": {}, "body": {}, "data": {}, "value": {}, "contents": {}, "filecontent": {},
	}
	_, okTool := writeTools[tool]
	_, okArg := contentArgs[arg]
	return okTool && okArg
}

// invalidToolArgs ports _invalid_tool_args.
func invalidToolArgs(input any) bool {
	m, ok := input.(map[string]any)
	if !ok {
		return false
	}
	for key, value := range m {
		if !isPathLikeArgName(key) {
			continue
		}
		if pathLikeArgLooksPolluted(strValue(value)) {
			return true
		}
	}
	return false
}

func isPathLikeArgName(name any) bool {
	key := toolAliasKey(name)
	switch key {
	case "path", "filepath", "filename", "targetfile", "file", "dir", "directory", "cwd", "workdir", "workingdirectory":
		return true
	}
	return false
}

func pathLikeArgLooksPolluted(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.ContainsRune(trimmed, 0) || pathPollutionRe.MatchString(trimmed) {
		return true
	}
	lowered := strings.ToLower(trimmed)
	for _, marker := range []string{
		"<![cdata[", "]]>", "xyml|", "qnml|", "tool_calls", "invoke name=",
		"parameter name=", "</parameter", "</invoke", "function.name:", "function.arguments:",
	} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// FilterInputForTool keeps only keys present in the tool schema's properties
// (or the whole input when the tool declares none). Port of
// _filter_input_for_tool.
func FilterInputForTool(name string, input map[string]any, tools []map[string]any) map[string]any {
	return filterInputForTool(name, input, tools)
}

func filterInputForTool(name string, input map[string]any, tools []map[string]any) map[string]any {
	properties := schemaProperties(toolSchema(name, tools))
	if properties == nil {
		out := make(map[string]any, len(input))
		for key, value := range input {
			out[key] = value
		}
		return out
	}
	out := map[string]any{}
	for key, value := range input {
		if _, ok := properties[key]; ok {
			out[key] = value
		}
	}
	return out
}

func toolAcceptsField(name string, tools any, field string) bool {
	properties := schemaProperties(toolSchema(name, tools))
	if properties == nil {
		return false
	}
	_, ok := properties[field]
	return ok
}

// renameFirstPresent ports _rename_first_present.
func renameFirstPresent(object map[string]any, canonical string, aliases ...string) {
	if value, ok := object[canonical]; ok && value != nil {
		return
	}
	for _, alias := range aliases {
		if value, ok := object[alias]; ok && value != nil {
			object[canonical] = value
			delete(object, alias)
			return
		}
	}
}

// strValue mirrors Python str() for the values flowing through the engine.
func strValue(value any) string { return jsonx.Str(value) }
