package fc

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/Sliverkiss/toolkit/engine"
	"github.com/Sliverkiss/toolkit/models"
)

// ReverseAliases is SAFE_TOOL_ALIASES inverted (canonical -> friendly).
var ReverseAliases = func() map[string]string {
	out := make(map[string]string, len(engine.SafeToolAliases))
	for alias, canonical := range engine.SafeToolAliases {
		out[canonical] = alias
	}
	return out
}()

var safeNameRe = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// ToSafeName cleans a tool name for obfuscation. Deliberately different from
// engine.safeToolName (see the Python comment). Port of to_safe_name.
func ToSafeName(name string) string {
	if canonical, ok := engine.SafeToolAliases[name]; ok {
		return canonical
	}
	if _, ok := ReverseAliases[name]; ok {
		return name
	}
	cleaned := strings.Trim(safeNameRe.ReplaceAllString(name, "_"), "_")
	if cleaned == "" {
		cleaned = "tool"
	}
	hasUpper := false
	for _, r := range cleaned[1:] {
		if unicode.IsUpper(r) {
			hasUpper = true
			break
		}
	}
	if unicode.IsUpper(rune(cleaned[0])) || hasUpper {
		return "u_" + cleaned
	}
	return cleaned
}

// FromSafeName restores an obfuscated name. Port of from_safe_name.
func FromSafeName(name string, originalNames map[string]string) string {
	if originalNames != nil {
		if original, ok := originalNames[name]; ok {
			return original
		}
	}
	if original, ok := ReverseAliases[name]; ok {
		return original
	}
	if strings.HasPrefix(name, "u_") && len(name) > 2 {
		return name[2:]
	}
	return name
}

// ObfuscateTools returns safe-named tools plus the safe→original map. Port of
// obfuscate_tools.
func ObfuscateTools(tools []models.ToolDef) ([]models.ToolDef, map[string]string) {
	mapping := map[string]string{}
	out := make([]models.ToolDef, 0, len(tools))
	for _, tool := range tools {
		safe := ToSafeName(tool.Name)
		mapping[safe] = tool.Name
		out = append(out, models.ToolDef{
			Name:        safe,
			Description: tool.Description,
			Parameters:  tool.Parameters,
			Raw:         map[string]any{},
		})
	}
	return out, mapping
}

// DeobfuscateCalls restores original tool names on parsed calls. Port of
// deobfuscate_calls.
func DeobfuscateCalls(calls []models.ToolCall, mapping map[string]string) []models.ToolCall {
	restored := make([]models.ToolCall, 0, len(calls))
	for _, call := range calls {
		restored = append(restored, models.ToolCall{
			ID:        call.ID,
			Name:      FromSafeName(call.Name, mapping),
			Arguments: call.Arguments,
		})
	}
	return restored
}
