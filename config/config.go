// Package config loads the ToolForge YAML configuration with ${ENV}
// interpolation. Port of app/config.py.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var envPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// ExpandEnv expands ${VAR} and ${VAR:-default} placeholders.
func ExpandEnv(value string) string {
	return envPattern.ReplaceAllStringFunc(value, func(match string) string {
		body := match[2 : len(match)-1]
		if idx := strings.Index(body, ":-"); idx >= 0 {
			name := strings.TrimSpace(body[:idx])
			fallback := body[idx+2:]
			if v, ok := os.LookupEnv(name); ok {
				return v
			}
			return fallback
		}
		return os.Getenv(strings.TrimSpace(body))
	})
}

// expandTree applies ExpandEnv to every string node (dict keys are untouched,
// as in Python).
func expandTree(node any) any {
	switch v := node.(type) {
	case string:
		return ExpandEnv(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = expandTree(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, value := range v {
			out[key] = expandTree(value)
		}
		return out
	default:
		return node
	}
}

// ServerConfig is the listener configuration.
type ServerConfig struct {
	Host           string
	Port           int
	TimeoutSeconds int
}

// ClientAuthConfig is the client key policy.
type ClientAuthConfig struct {
	Enabled     bool
	AllowedKeys []string
}

// UpstreamConfig describes one upstream endpoint.
type UpstreamConfig struct {
	Name      string
	Type      string
	BaseURL   string
	APIKey    string
	Models    []string
	NativeFC  bool
	IsDefault bool
	Headers   map[string]string
}

// FeaturesConfig toggles gateway features.
type FeaturesConfig struct {
	FCMode                   string
	EnableStreaming          bool
	EnableFCErrorRetry       bool
	FCErrorRetryMax          int
	StripThinkTags           bool
	InjectProtocol           string
	MissingRequired          string
	ConvertDeveloperToSystem bool
	KeyPassthrough           bool
	ModelPassthrough         bool
	SanitizeClaudeCode       bool
	EnableCLIProfiles        bool
	ObfuscateToolNames       bool
	EnableMetrics            bool
}

// RoutingConfig holds model aliases.
type RoutingConfig struct {
	Aliases map[string]map[string]string
}

// AppConfig is the whole configuration tree.
type AppConfig struct {
	Server               ServerConfig
	ClientAuthentication ClientAuthConfig
	Upstreams            []UpstreamConfig
	Routing              RoutingConfig
	Features             FeaturesConfig
}

// DefaultUpstream returns the is_default upstream, else the first one.
func (c *AppConfig) DefaultUpstream() *UpstreamConfig {
	for i := range c.Upstreams {
		if c.Upstreams[i].IsDefault {
			return &c.Upstreams[i]
		}
	}
	if len(c.Upstreams) > 0 {
		return &c.Upstreams[0]
	}
	return nil
}

// FindUpstream looks up an upstream by name.
func (c *AppConfig) FindUpstream(name string) *UpstreamConfig {
	for i := range c.Upstreams {
		if c.Upstreams[i].Name == name {
			return &c.Upstreams[i]
		}
	}
	return nil
}

// ResolveModel maps a client model name to (upstream, upstream model).
func (c *AppConfig) ResolveModel(model string) (*UpstreamConfig, string, error) {
	if alias, ok := c.Routing.Aliases[model]; ok {
		upstreamName := alias["upstream"]
		upstreamModel := alias["model"]
		if upstreamModel == "" {
			upstreamModel = model
		}
		upstream := c.FindUpstream(upstreamName)
		if upstream == nil {
			return nil, "", fmt.Errorf("routing alias '%s' points to unknown upstream '%s'", model, upstreamName)
		}
		return upstream, upstreamModel, nil
	}
	for i := range c.Upstreams {
		if contains(c.Upstreams[i].Models, model) {
			return &c.Upstreams[i], model, nil
		}
	}
	for i := range c.Upstreams {
		if contains(c.Upstreams[i].Models, "*") {
			return &c.Upstreams[i], model, nil
		}
	}
	if def := c.DefaultUpstream(); def != nil {
		return def, model, nil
	}
	return nil, "", fmt.Errorf("no upstream configured for model '%s'", model)
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

// Load reads and validates a YAML config file.
func Load(path string) (*AppConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config not found: %s", path)
		}
		return nil, err
	}
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if raw == nil {
		raw = map[string]any{}
	}
	expanded := expandTree(raw)
	root, ok := expanded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("config root must be a mapping")
	}
	return fromMap(root)
}

func fromMap(data map[string]any) (*AppConfig, error) {
	serverRaw := asDict(data["server"])
	authRaw := asDict(data["client_authentication"])
	featuresRaw := asDict(data["features"])
	routingRaw := asDict(data["routing"])

	upstreams := []UpstreamConfig{}
	for _, item := range asList(data["upstreams"]) {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := strings.TrimSpace(asString(m["name"]))
		if name == "" {
			name = "unnamed"
		}
		upstreamType := strings.TrimSpace(asString(m["type"]))
		if upstreamType == "" {
			upstreamType = "openai_compat"
		}
		models := []string{"*"}
		if rawModels, ok := m["models"].([]any); ok {
			models = make([]string, len(rawModels))
			for i, model := range rawModels {
				models[i] = asString(model)
			}
		}
		headers := map[string]string{}
		for key, value := range asDict(m["headers"]) {
			headers[asString(key)] = asString(value)
		}
		upstreams = append(upstreams, UpstreamConfig{
			Name:      name,
			Type:      upstreamType,
			BaseURL:   strings.TrimRight(asString(m["base_url"]), "/"),
			APIKey:    asString(m["api_key"]),
			Models:    models,
			NativeFC:  boolValue(m["native_fc"], true),
			IsDefault: boolValue(m["is_default"], false),
			Headers:   headers,
		})
	}

	aliases := map[string]map[string]string{}
	for key, value := range asDict(routingRaw["aliases"]) {
		if m, ok := value.(map[string]any); ok {
			entry := make(map[string]string, len(m))
			for k, v := range m {
				entry[asString(k)] = asString(v)
			}
			aliases[asString(key)] = entry
		}
	}

	port, err := intValue(serverRaw["port"], 8080)
	if err != nil {
		return nil, err
	}
	timeout, err := intValue(serverRaw["timeout_seconds"], 180)
	if err != nil {
		return nil, err
	}
	retryMax, err := intValue(featuresRaw["fc_error_retry_max"], 2)
	if err != nil {
		return nil, err
	}
	host := asString(serverRaw["host"])
	if host == "" {
		host = "0.0.0.0"
	}
	allowedKeys := []string{}
	if rawKeys, ok := authRaw["allowed_keys"].([]any); ok {
		for _, key := range rawKeys {
			allowedKeys = append(allowedKeys, asString(key))
		}
	}
	return &AppConfig{
		Server: ServerConfig{
			Host:           host,
			Port:           port,
			TimeoutSeconds: timeout,
		},
		ClientAuthentication: ClientAuthConfig{
			Enabled:     boolValue(authRaw["enabled"], true),
			AllowedKeys: allowedKeys,
		},
		Upstreams: upstreams,
		Routing:   RoutingConfig{Aliases: aliases},
		Features: FeaturesConfig{
			FCMode:                   defaultString(featuresRaw["fc_mode"], "auto"),
			EnableStreaming:          boolValue(featuresRaw["enable_streaming"], true),
			EnableFCErrorRetry:       boolValue(featuresRaw["enable_fc_error_retry"], true),
			FCErrorRetryMax:          retryMax,
			StripThinkTags:           boolValue(featuresRaw["strip_think_tags"], true),
			InjectProtocol:           defaultString(featuresRaw["inject_protocol"], "XYML"),
			MissingRequired:          defaultString(featuresRaw["missing_required"], "defaulted"),
			ConvertDeveloperToSystem: boolValue(featuresRaw["convert_developer_to_system"], true),
			KeyPassthrough:           boolValue(featuresRaw["key_passthrough"], false),
			ModelPassthrough:         boolValue(featuresRaw["model_passthrough"], false),
			SanitizeClaudeCode:       boolValue(featuresRaw["sanitize_claude_code"], true),
			EnableCLIProfiles:        boolValue(featuresRaw["enable_cli_profiles"], true),
			ObfuscateToolNames:       boolValue(featuresRaw["obfuscate_tool_names"], false),
			EnableMetrics:            boolValue(featuresRaw["enable_metrics"], true),
		},
	}, nil
}

// DefaultPath resolves the config path (TOOLFORGE_CONFIG, cwd/config.yaml,
// then next to the executable). Port of default_config_path.
func DefaultPath() string {
	if env := os.Getenv("TOOLFORGE_CONFIG"); env != "" {
		return env
	}
	if cwd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(cwd, "config.yaml")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(filepath.Dir(exe)), "config.example.yaml")
	}
	return "config.example.yaml"
}

func asDict(value any) map[string]any {
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func asList(value any) []any {
	if list, ok := value.([]any); ok {
		return list
	}
	return nil
}

func asString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case int:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	case float64:
		return fmt.Sprintf("%g", v)
	case bool:
		if v {
			return "True"
		}
		return "False"
	default:
		return ""
	}
}

func defaultString(value any, fallback string) string {
	trimmed := strings.TrimSpace(asString(value))
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

// boolValue mirrors Python's bool(value) with a default, including YAML 1.1
// string forms ("true"/"yes"/"on"/"1") and numbers.
func boolValue(value any, fallback bool) bool {
	switch v := value.(type) {
	case nil:
		return fallback
	case bool:
		return v
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "yes", "on", "1":
			return true
		case "false", "no", "off", "0", "":
			return false
		}
		return true
	default:
		return true
	}
}

// intValue mirrors Python's int(value or fallback); non-numeric values error.
func intValue(value any, fallback int) (int, error) {
	switch v := value.(type) {
	case nil:
		return fallback, nil
	case int:
		if v == 0 {
			return fallback, nil
		}
		return v, nil
	case int64:
		if v == 0 {
			return fallback, nil
		}
		return int(v), nil
	case float64:
		if v == 0 {
			return fallback, nil
		}
		return int(v), nil
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return fallback, nil
		}
		var parsed int
		if _, err := fmt.Sscanf(trimmed, "%d", &parsed); err != nil {
			return 0, fmt.Errorf("invalid integer %q", v)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("invalid integer %v", value)
	}
}
