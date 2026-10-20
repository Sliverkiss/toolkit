// Package fc is the Go port of app/fc/: prompt-FC injection, parsing,
// sanitization, recovery, name obfuscation, mode policy and tool profiles.
package fc

import (
	"sync"

	"github.com/Sliverkiss/toolkit/engine"
)

// DefaultProtocol is the protocol injected when none is configured.
const DefaultProtocol = "XYML"

// configKey identifies a cached ToolCallConfig.
type configKey struct {
	protocol        string
	missingRequired string
}

var (
	configMu    sync.Mutex
	configCache = map[configKey]*engine.ToolCallConfig{}
)

// Config returns a shared prompt-FC config (Python's lru_cache(maxsize=8)).
func Config(protocol, missingRequired string) *engine.ToolCallConfig {
	if protocol == "" {
		protocol = DefaultProtocol
	}
	if missingRequired == "" {
		missingRequired = "defaulted"
	}
	key := configKey{protocol: protocol, missingRequired: missingRequired}
	configMu.Lock()
	defer configMu.Unlock()
	if cached, ok := configCache[key]; ok {
		return cached
	}
	config := engine.ForPrompt(protocol, missingRequired)
	configCache[key] = config
	return config
}
