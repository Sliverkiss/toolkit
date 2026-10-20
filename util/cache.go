package util

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/Sliverkiss/toolkit/jsonx"
)

// cacheEntry mirrors Python's {prompt_hash: (timestamp, prompt_tokens)}.
type cacheEntry struct {
	ts     float64
	tokens int
}

var (
	cacheMu     sync.Mutex
	promptCache = map[string]cacheEntry{}
	cacheTTL    = 3600.0
	nowFn       = func() float64 { return float64(time.Now().UnixNano()) / 1e9 }
)

// ResetCache clears the prompt cache (tests only). Port of _reset_cache.
func ResetCache() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	promptCache = map[string]cacheEntry{}
}

// HashPrompt returns the first 32 hex chars of SHA-256. Port of _hash_prompt.
func HashPrompt(promptText string) string {
	sum := sha256.Sum256([]byte(promptText))
	return hex.EncodeToString(sum[:])[:32]
}

func cleanupExpiredLocked() {
	now := nowFn()
	for k, entry := range promptCache {
		if now-entry.ts > cacheTTL {
			delete(promptCache, k)
		}
	}
}

// EstimateCacheTokens reports local cache statistics for a prompt. The second
// request with the same prompt is treated as a cache hit. Port of
// estimate_cache_tokens.
func EstimateCacheTokens(promptText string, promptTokens int) map[string]int {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cleanupExpiredLocked()

	key := HashPrompt(promptText)
	now := nowFn()
	if entry, ok := promptCache[key]; ok {
		return map[string]int{
			"cached_tokens":               entry.tokens,
			"cache_read_input_tokens":     entry.tokens,
			"cache_creation_input_tokens": 0,
			"prompt_cache_hit_tokens":     entry.tokens,
			"prompt_cache_miss_tokens":    0,
			"prompt_cache_write_tokens":   0,
		}
	}
	promptCache[key] = cacheEntry{ts: now, tokens: promptTokens}
	return map[string]int{
		"cached_tokens":               0,
		"cache_read_input_tokens":     0,
		"cache_creation_input_tokens": promptTokens,
		"prompt_cache_hit_tokens":     0,
		"prompt_cache_miss_tokens":    promptTokens,
		"prompt_cache_write_tokens":   promptTokens,
	}
}

// EnrichUsage adds local cache estimation to a usage map. Upstream cache values
// win; missing fields are filled locally. Mutates and returns usage (Python
// semantics). Port of enrich_usage.
func EnrichUsage(usage map[string]any, promptText string, promptTokens int) map[string]any {
	if usage == nil {
		usage = map[string]any{}
	}
	stats := EstimateCacheTokens(promptText, promptTokens)
	for key, value := range stats {
		if existing, ok := usage[key]; !ok || jsonx.Falsy(existing) {
			usage[key] = value
		}
	}
	details, ok := usage["prompt_tokens_details"].(map[string]any)
	if !ok {
		details = map[string]any{}
		usage["prompt_tokens_details"] = details
	}
	if cached, ok := details["cached_tokens"]; !ok || jsonx.Falsy(cached) {
		details["cached_tokens"] = usage["cached_tokens"]
		if details["cached_tokens"] == nil {
			details["cached_tokens"] = 0
		}
	}
	if _, ok := usage["completion_tokens_details"]; !ok {
		usage["completion_tokens_details"] = map[string]any{
			"accepted_prediction_tokens": 0,
			"audio_tokens":               0,
			"reasoning_tokens":           0,
			"rejected_prediction_tokens": 0,
			"cached_tokens":              0,
		}
	}
	return usage
}
