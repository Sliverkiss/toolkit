package engine

import (
	"strings"

	"github.com/Sliverkiss/toolkit/util"
)

// BuildLegacyNameMap returns {normalized legacy spelling: registered name} for
// the subset where the friendly name is actually registered. Port of
// build_legacy_name_map (Ticket 03).
func BuildLegacyNameMap(registeredNames []string) map[string]string {
	reg := make(map[string]struct{}, len(registeredNames))
	for _, name := range registeredNames {
		if name != "" {
			reg[toolAliasKey(name)] = struct{}{}
		}
	}
	out := map[string]string{}
	for legacy, newName := range legacyToolName {
		if _, ok := reg[toolAliasKey(newName)]; ok {
			out[toolAliasKey(legacy)] = newName
		}
	}
	return out
}

// CanonicalizeResponseToolName rewrites a hallucinated legacy spelling to the
// registered name; everything else passes through. Port of
// canonicalize_response_tool_name.
func CanonicalizeResponseToolName(name any, legacyMap map[string]string) string {
	raw := jsonStr(name)
	if mapped, ok := legacyMap[toolAliasKey(raw)]; ok {
		return mapped
	}
	return raw
}

func randomID(n int) string { return util.RandomID(n) }

func jsonStr(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return strOf(value)
}

// strOf mirrors Python str() for the scalar types the engine passes around.
func strOf(value any) string {
	return strValue(value)
}

// buildAllowedToolMap ports _build_allowed_tool_map.
func buildAllowedToolMap(tools []map[string]any, config *ToolCallConfig) map[string]string {
	allowed := map[string]string{}
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		if name == "" {
			continue
		}
		allowed[toolAliasKey(name)] = name
	}
	for alias, canonical := range SafeToolAliases {
		if _, ok := allowed[toolAliasKey(alias)]; !ok {
			allowed[toolAliasKey(alias)] = canonical
		}
	}
	for canonical, alias := range config.ToolAliases {
		key := toolAliasKey(canonical)
		aliasKey := toolAliasKey(alias)
		resolved, ok := allowed[key]
		if !ok {
			resolved = canonical
		}
		if _, ok := allowed[aliasKey]; !ok {
			allowed[aliasKey] = resolved
		}
		if _, ok := allowed[key]; !ok {
			allowed[key] = resolved
		}
	}
	return allowed
}

// canonicalToolName ports _canonical_tool_name.
func canonicalToolName(name any, allowed map[string]string, config *ToolCallConfig) string {
	raw, _ := name.(string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if direct, ok := allowed[toolAliasKey(raw)]; ok && direct != "" {
		return direct
	}
	configured := config.ToolAliases[raw]
	if configured == "" {
		configured = config.ToolAliases[strings.ToLower(raw)]
	}
	if configured != "" {
		if hit, ok := allowed[toolAliasKey(configured)]; ok && hit != "" {
			return hit
		}
	}
	if strings.HasPrefix(raw, "u_") {
		candidate := raw[2:]
		if candidate != "" {
			if hit, ok := allowed[toolAliasKey(candidate)]; ok {
				return hit
			}
		}
	}
	if config.UnknownTool == "drop" {
		return ""
	}
	return raw
}

// safeToolName ports _safe_tool_name (instruction rendering only).
func safeToolName(name any) string {
	trimmed := strings.TrimSpace(strOf(name))
	if trimmed == "" {
		return ""
	}
	if alias, ok := SafeToolAliases[trimmed]; ok {
		return alias
	}
	for _, alias := range SafeToolAliases {
		if strings.EqualFold(alias, trimmed) {
			return trimmed
		}
	}
	if strings.HasPrefix(trimmed, "u_") {
		return trimmed
	}
	return "u_" + trimmed
}
