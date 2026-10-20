// Package engine is the Go port of app/engine/xyml.py: the XYML protocol
// grammar, parser, renderer and streaming sieve, plus a small facade.
package engine

import (
	"sort"
	"strings"

	"github.com/Sliverkiss/toolkit/util"
)

// DefaultRawStringParams are parameter names whose values are never JSON/bool
// coerced. Port of DEFAULT_RAW_STRING_PARAMS.
var DefaultRawStringParams = map[string]struct{}{
	"content": {}, "command": {}, "cmd": {}, "script": {}, "code": {}, "prompt": {},
	"file_content": {}, "old_string": {}, "new_string": {}, "insert_text": {},
	"patch": {}, "pattern": {}, "text": {}, "query": {}, "url": {}, "path": {},
	"file_path": {},
}

// DefaultToolAliases maps legacy canonical spellings to friendly aliases.
var DefaultToolAliases = map[string]string{
	"fs_open_file":   "Read",
	"fs_put_file":    "Write",
	"fs_patch_file":  "Edit",
	"shell_run":      "Bash",
	"text_search":    "Grep",
	"path_find":      "Glob",
	"notebook_patch": "NotebookEdit",
	"http_get_url":   "WebFetch",
	"web_query":      "WebSearch",
}

// SafeToolAliases maps friendly aliases to legacy canonical spellings.
var SafeToolAliases = map[string]string{
	"Read":         "fs_open_file",
	"Write":        "fs_put_file",
	"Edit":         "fs_patch_file",
	"Bash":         "shell_run",
	"Grep":         "text_search",
	"Glob":         "path_find",
	"NotebookEdit": "notebook_patch",
	"WebFetch":     "http_get_url",
	"WebSearch":    "web_query",
}

// legacyToolName maps hallucinated legacy spellings to registered names.
var legacyToolName = map[string]string{
	"shell_run":      "Bash",
	"fs_open_file":   "Read",
	"fs_put_file":    "Write",
	"fs_patch_file":  "Edit",
	"text_search":    "Grep",
	"path_find":      "Glob",
	"notebook_patch": "NotebookEdit",
	"http_get_url":   "WebFetch",
	"web_query":      "WebSearch",
}

// MarkupReplacements normalizes fullwidth/Unicode variant characters.
var MarkupReplacements = [][2]string{
	{"＜", "<"}, {"＞", ">"}, {"／", "/"}, {"∕", "/"}, {"⁄", "/"},
	{"＝", "="}, {"：", ":"}, {"｜", "|"}, {"│", "|"}, {"┃", "|"},
	{"▏", "|"}, {"▕", "|"}, {"“", `"`}, {"”", `"`}, {"‘", "'"},
	{"’", "'"}, {"﹤", "<"}, {"﹥", ">"},
}

// ParsedToolCall is a normalized function call extracted from model output.
type ParsedToolCall struct {
	Name  string
	Input any
	ID    string
}

// ProtocolSpec names one markup tool-call protocol.
type ProtocolSpec struct {
	Name      string
	Root      string
	Invoke    string
	Parameter string
}

// NewProtocolSpec builds a protocol spec with the standard default tags.
func NewProtocolSpec(name string) ProtocolSpec {
	return ProtocolSpec{Name: strings.TrimSpace(name), Root: "tool_calls", Invoke: "invoke", Parameter: "parameter"}
}

// NewProtocolSpecWithTags builds a spec overriding individual tags.
func NewProtocolSpecWithTags(name string, tags map[string]string) ProtocolSpec {
	spec := NewProtocolSpec(name)
	if tags == nil {
		return spec
	}
	if v, ok := tags["root"]; ok {
		spec.Root = v
	}
	if v, ok := tags["invoke"]; ok {
		spec.Invoke = v
	}
	if v, ok := tags["parameter"]; ok {
		spec.Parameter = v
	}
	return spec
}

// Options configures a ToolCallConfig. Zero values select the Python defaults;
// boolean options use pointers so "unset" is distinguishable from false.
type Options struct {
	EmitProtocol    string
	ParseProtocols  []ProtocolSpec
	Strict          *bool
	UnknownTool     string
	MissingRequired string
	EnableMarkup    *bool
	EnableXML       *bool
	EnableJSON      *bool
	EnableTextKV    *bool
	EnableCoercion  *bool
	EnableDedupe    *bool
	PromptStyle     string
	ToolAliases     map[string]string
	ArgumentAliases map[string]map[string][]string
	RawStringParams []string
	IDFactory       func() string
}

// ToolCallConfig holds parser, compatibility and validation policies.
type ToolCallConfig struct {
	EmitProtocol    string
	ParseProtocols  []ProtocolSpec
	Strict          bool
	UnknownTool     string
	MissingRequired string
	EnableMarkup    bool
	EnableXML       bool
	EnableJSON      bool
	EnableTextKV    bool
	EnableCoercion  bool
	EnableDedupe    bool
	PromptStyle     string
	ToolAliases     map[string]string
	ArgumentAliases map[string]map[string][]string
	RawStringParams map[string]struct{}
	IDFactory       func() string
}

// DefaultConfig returns a config with Python's dataclass defaults.
func DefaultConfig() *ToolCallConfig {
	return NewConfig(Options{})
}

// NewConfig constructs a ToolCallConfig from options.
func NewConfig(opts Options) *ToolCallConfig {
	emit := strings.TrimSpace(opts.EmitProtocol)
	if emit == "" {
		emit = "XYML"
	}
	protocols := opts.ParseProtocols
	if protocols == nil {
		protocols = []ProtocolSpec{NewProtocolSpec(emit), NewProtocolSpec("QNML")}
	}
	unknown := opts.UnknownTool
	if unknown == "" {
		unknown = "drop"
	}
	missing := opts.MissingRequired
	if missing == "" {
		missing = "drop"
	}
	style := opts.PromptStyle
	if style == "" {
		style = "standard"
	}
	aliases := make(map[string]string, len(DefaultToolAliases)+len(opts.ToolAliases))
	for k, v := range DefaultToolAliases {
		aliases[k] = v
	}
	for k, v := range opts.ToolAliases {
		aliases[k] = v
	}
	raw := make(map[string]struct{}, len(DefaultRawStringParams)+len(opts.RawStringParams))
	for k := range DefaultRawStringParams {
		raw[k] = struct{}{}
	}
	for _, value := range opts.RawStringParams {
		raw[strings.ToLower(value)] = struct{}{}
	}
	idFactory := opts.IDFactory
	if idFactory == nil {
		idFactory = util.CallID
	}
	argumentAliases := opts.ArgumentAliases
	if argumentAliases == nil {
		argumentAliases = map[string]map[string][]string{}
	}
	return &ToolCallConfig{
		EmitProtocol:    emit,
		ParseProtocols:  normalizeProtocolSpecs(protocols, emit),
		Strict:          boolOpt(opts.Strict, false),
		UnknownTool:     unknown,
		MissingRequired: missing,
		EnableMarkup:    boolOpt(opts.EnableMarkup, true),
		EnableXML:       boolOpt(opts.EnableXML, true),
		EnableJSON:      boolOpt(opts.EnableJSON, true),
		EnableTextKV:    boolOpt(opts.EnableTextKV, true),
		EnableCoercion:  boolOpt(opts.EnableCoercion, true),
		EnableDedupe:    boolOpt(opts.EnableDedupe, true),
		PromptStyle:     style,
		ToolAliases:     aliases,
		ArgumentAliases: argumentAliases,
		RawStringParams: raw,
		IDFactory:       idFactory,
	}
}

// ForPrompt builds the prompt-FC config with XYML/QNML/DSML parse protocols.
// Port of ToolCallConfig.for_prompt.
func ForPrompt(protocol, missingRequired string) *ToolCallConfig {
	emit := strings.TrimSpace(protocol)
	if emit == "" {
		emit = "XYML"
	}
	if missingRequired == "" {
		missingRequired = "defaulted"
	}
	return NewConfig(Options{
		EmitProtocol:    emit,
		ParseProtocols:  []ProtocolSpec{NewProtocolSpec(emit), NewProtocolSpec("QNML"), NewProtocolSpec("XYML"), NewProtocolSpec("DSML")},
		MissingRequired: missingRequired,
	})
}

func boolOpt(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func normalizeProtocolSpecs(values []ProtocolSpec, emit string) []ProtocolSpec {
	out := make([]ProtocolSpec, 0, len(values)+1)
	seen := map[string]struct{}{}
	for _, spec := range values {
		key := strings.ToLower(spec.Name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, spec)
	}
	if _, ok := seen[strings.ToLower(emit)]; !ok {
		out = append([]ProtocolSpec{NewProtocolSpec(emit)}, out...)
	}
	return out
}

// sortedKeys returns map keys sorted for deterministic rendering.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
