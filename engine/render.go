package engine

import (
	"strings"
)

// BuildToolInstructions renders the XYML system-prompt block. Port of
// build_tool_instructions.
func BuildToolInstructions(tools any, config *ToolCallConfig, protocol *ProtocolSpec) string {
	if config == nil {
		config = DefaultConfig()
	}
	active := resolveProtocol(protocol, config.EmitProtocol)
	normalized := NormalizeTools(tools)
	safeTools := make([]map[string]any, len(normalized))
	names := make([]string, 0, len(normalized))
	for i, tool := range normalized {
		copied := make(map[string]any, len(tool)+1)
		for key, value := range tool {
			copied[key] = value
		}
		copied["name"] = safeToolName(tool["name"])
		safeTools[i] = copied
		if name, ok := copied["name"].(string); ok && name != "" {
			names = append(names, name)
		}
	}
	schemas := make([]string, 0, len(safeTools))
	for _, tool := range safeTools {
		parameters := tool["parameters"]
		if parameters == nil {
			parameters = tool["input_schema"]
		}
		schemas = append(schemas, strings.Join([]string{
			"Action name: " + safeName(tool["name"]),
			"Description: " + clip(tool["description"], 240),
			"Parameters: " + summarizeSchema(parameters),
		}, "\n"))
	}
	exampleTools := safeTools
	if len(exampleTools) > 2 {
		exampleTools = exampleTools[:2]
	}
	if len(exampleTools) == 0 {
		exampleTools = []map[string]any{{
			"name": "TOOL_NAME",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{"ARG": map[string]any{"type": "string"}},
			},
		}}
	}
	examples := make([]string, 0, len(exampleTools))
	for _, tool := range exampleTools {
		examples = append(examples, RenderToolCall(tool["name"], exampleInputFromTool(tool), config, &active))
	}
	acceptedNames := make([]string, 0, len(config.ParseProtocols))
	for _, spec := range config.ParseProtocols {
		acceptedNames = append(acceptedNames, spec.Name)
	}
	schemaBlock := ""
	if len(schemas) > 0 {
		schemaBlock = "You have access to these tools:\n\n" + strings.Join(schemas, "\n\n") + "\n\n"
	}
	defensiveRules := ""
	if config.PromptStyle != "minimal" {
		defensiveRules = `
RULES:
1. If a tool is needed, output a parseable ` + active.Name + ` tool-call block. If no tool is needed, answer normally.
2. Use exact action names and parameter names from the schema.
3. Strings should use <![CDATA[...]]>; objects may use JSON or nested XML-like values; arrays may use JSON arrays or repeated <item> nodes.
4. Never emit empty required parameters. Ask normally if required information is unknown.
5. After a tool result, call another tool only if needed; otherwise answer normally.
6. Path-like parameters must contain only the path string, not prose or protocol fragments.
`
	}
	renderedFormat := RenderToolCall("TOOL_NAME", map[string]any{"ARG": "value"}, config, &active)
	return "=== " + active.Name + " TOOL CALL PROTOCOL ===\n" +
		schemaBlock +
		"Default protocol for new tool calls: " + active.Name + "\n" +
		"Accepted parse protocols by this client: " + strings.Join(acceptedNames, ", ") + "\n" +
		"Available action names: " + strings.Join(names, ", ") + "\n\n" +
		"FORMAT:\n" + renderedFormat + "\n" + defensiveRules + "\n" +
		"CORRECT EXAMPLES:\n\n" + strings.Join(examples, "\n\n") + "\n\n" +
		"Remember: the preferred tool-call form is <|" + active.Name + "|tool_calls>...</|" + active.Name + "|tool_calls>.\n" +
		"=== END " + active.Name + " TOOL INSTRUCTIONS ==="
}

func safeName(value any) string {
	s, _ := value.(string)
	return s
}

// RenderToolCall renders one call in the configured markup protocol. Port of
// render_tool_call.
func RenderToolCall(name any, input any, config *ToolCallConfig, protocol *ProtocolSpec) string {
	if config == nil {
		config = DefaultConfig()
	}
	active := resolveProtocol(protocol, config.EmitProtocol)
	callName := strings.TrimSpace(strValue(name))
	if callName == "" {
		return ""
	}
	arguments, ok := input.(map[string]any)
	if !ok {
		arguments = map[string]any{"input": input}
	}
	lines := []string{
		"<|" + active.Name + "|" + active.Root + ">",
		`  <|` + active.Name + "|" + active.Invoke + ` name="` + escapeXML(callName) + `">`,
	}
	for _, key := range sortedKeys(arguments) {
		lines = append(lines, "    <|"+active.Name+"|"+active.Parameter+` name="`+escapeXML(key)+`">`+
			renderMarkupValue(arguments[key])+"</|"+active.Name+"|"+active.Parameter+">")
	}
	lines = append(lines,
		"  </|"+active.Name+"|"+active.Invoke+">",
		"</|"+active.Name+"|"+active.Root+">",
	)
	return strings.Join(lines, "\n")
}

func resolveProtocol(protocol *ProtocolSpec, fallback string) ProtocolSpec {
	if protocol != nil {
		return *protocol
	}
	return NewProtocolSpec(fallback)
}

func renderMarkupValue(value any) string {
	if value == nil {
		return "null"
	}
	if s, ok := value.(string); ok {
		return "<![CDATA[" + strings.ReplaceAll(s, "]]>", "]]]]><![CDATA[>") + "]]>"
	}
	if b, ok := value.(bool); ok {
		if b {
			return "true"
		}
		return "false"
	}
	return jsonDumps(value)
}

func exampleInputFromTool(tool map[string]any) map[string]any {
	parameters := tool["parameters"]
	if parameters == nil {
		parameters = tool["input_schema"]
	}
	properties := schemaProperties(parameters)
	if len(properties) == 0 {
		return map[string]any{"ARG": "value"}
	}
	example := map[string]any{}
	count := 0
	for _, key := range sortedKeys(properties) {
		if count >= 3 {
			break
		}
		example[key] = exampleValue(properties[key])
		count++
	}
	if len(example) == 0 {
		return map[string]any{"ARG": "value"}
	}
	return example
}

func exampleValue(schema any) any {
	kinds := schemaTypes(schema)
	if _, ok := kinds["array"]; ok {
		return []any{}
	}
	if _, ok := kinds["object"]; ok {
		return map[string]any{}
	}
	if _, ok := kinds["boolean"]; ok {
		return true
	}
	if _, ok := kinds["number"]; ok {
		return 1
	}
	if _, ok := kinds["integer"]; ok {
		return 1
	}
	return "value"
}

func summarizeSchema(schema any) string {
	if schema == nil {
		return "{}"
	}
	if m, ok := schema.(map[string]any); ok && len(m) == 0 {
		return "{}"
	}
	return jsonDumps(schema)
}
