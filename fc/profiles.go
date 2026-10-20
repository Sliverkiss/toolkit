package fc

import (
	"regexp"
	"strings"

	"github.com/Sliverkiss/toolkit/models"
)

// ToolProfile is a detected CLI tool family. Port of the ToolProfile dataclass.
type ToolProfile struct {
	ID          string
	DisplayName string
	Rules       []string
}

var profileNameRe = regexp.MustCompile(`[^a-z0-9]+`)

func normName(name string) string {
	return profileNameRe.ReplaceAllString(strings.ToLower(name), "")
}

func toolNameSet(tools []models.ToolDef) map[string]struct{} {
	out := map[string]struct{}{}
	for _, tool := range tools {
		if tool.Name != "" {
			out[normName(tool.Name)] = struct{}{}
		}
	}
	return out
}

func intersects(names map[string]struct{}, candidates ...string) bool {
	for _, candidate := range candidates {
		if _, ok := names[candidate]; ok {
			return true
		}
	}
	return false
}

func containsAll(names map[string]struct{}, candidates ...string) bool {
	for _, candidate := range candidates {
		if _, ok := names[candidate]; !ok {
			return false
		}
	}
	return true
}

// DetectToolProfile identifies the CLI family. Port of detect_tool_profile.
func DetectToolProfile(tools []models.ToolDef) ToolProfile {
	names := toolNameSet(tools)
	if intersects(names, "skillslist", "skillview") || containsAll(names, "readfile", "terminal", "writefile") {
		return ToolProfile{
			ID: "hermes", DisplayName: "Hermes",
			Rules: []string{
				"Use exact Hermes tool names from the list; never invent Claude Code names.",
				"Prefer skills_list/skill_view when exploring skills.",
			},
		}
	}
	if intersects(names, "sessionsspawn", "exec") || (has(names, "process") && has(names, "exec")) {
		return ToolProfile{
			ID: "openclaw", DisplayName: "OpenClaw",
			Rules: []string{
				"Map file work to available read/write/exec tools only.",
				"Use process only for background job control.",
			},
		}
	}
	if containsAll(names, "bash", "read", "write") || intersects(names, "task", "agent") {
		return ToolProfile{
			ID: "claude_code", DisplayName: "Claude Code",
			Rules: []string{
				"Use exact PascalCase tool names (Read/Write/Bash/...) as listed.",
				"Prefer direct tools over Task/Agent when a single step suffices.",
			},
		}
	}
	if intersects(names, "bash", "read", "write", "edit") && has(names, "todowrite") {
		return ToolProfile{
			ID: "opencode", DisplayName: "OpenCode",
			Rules: []string{
				"Use exact lowercase tool names from the list.",
				"todowrite is for planning only, not for file changes.",
			},
		}
	}
	return ToolProfile{
		ID: "generic", DisplayName: "Generic CLI",
		Rules: []string{"Use the exact action names listed; do not translate to another CLI's names."},
	}
}

func has(names map[string]struct{}, key string) bool {
	_, ok := names[key]
	return ok
}

// ProfileInstructionBlock renders the profile header for injection. Port of
// profile_instruction_block.
func ProfileInstructionBlock(profile ToolProfile) string {
	lines := append([]string{"[CLIENT TOOL PROFILE: " + profile.DisplayName + "]"}, profile.Rules...)
	return strings.Join(lines, "\n")
}

// BuildFewShotBlock renders a minimal few-shot example block. Port of
// build_few_shot_block.
func BuildFewShotBlock(tools []models.ToolDef, maxExamples int) string {
	if maxExamples <= 0 {
		maxExamples = 2
	}
	skip := []string{"todo", "task", "agent", "cron", "skill"}
	var picked []models.ToolDef
	for _, tool := range tools {
		key := normName(tool.Name)
		skipIt := false
		for _, s := range skip {
			if strings.Contains(key, s) {
				skipIt = true
				break
			}
		}
		if skipIt {
			continue
		}
		picked = append(picked, tool)
		if len(picked) >= maxExamples {
			break
		}
	}
	if len(picked) < 1 {
		return ""
	}
	lines := []string{"[FEW-SHOT]", "When you need a tool, emit the protocol block immediately."}
	for _, tool := range picked {
		sampleArgs := ""
		if tool.Parameters != nil {
			if properties, ok := tool.Parameters["properties"].(map[string]any); ok {
				var keys []string
				count := 0
				for key := range properties {
					if count >= 2 {
						break
					}
					keys = append(keys, key)
					count++
				}
				sortStrings(keys)
				parts := make([]string, 0, len(keys))
				for _, key := range keys {
					parts = append(parts, key+`="..."`)
				}
				sampleArgs = strings.Join(parts, ", ")
			}
		}
		if sampleArgs == "" {
			sampleArgs = "..."
		}
		lines = append(lines, "- Example tool: "+tool.Name+"("+sampleArgs+")")
	}
	return strings.Join(lines, "\n")
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
