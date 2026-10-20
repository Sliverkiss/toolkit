package fc

import (
	"strings"

	"github.com/Sliverkiss/toolkit/config"
	"github.com/Sliverkiss/toolkit/models"
)

// ResolveFCMode chooses the concrete path for one request. Port of
// resolve_fc_mode.
func ResolveFCMode(req *models.CanonicalRequest, upstream config.UpstreamConfig, features config.FeaturesConfig, headerOverride string) models.ResolvedFCMode {
	if len(req.Tools) == 0 {
		return models.ResolvedPassthrough
	}
	override := strings.TrimSpace(headerOverride)
	if override == "" {
		override = strings.TrimSpace(req.RequestFCOverride)
	}
	override = strings.ToLower(override)
	globalMode := strings.ToLower(strings.TrimSpace(features.FCMode))
	if globalMode == "" {
		globalMode = "auto"
	}
	switch override {
	case "force_prompt", "prompt":
		return models.ResolvedPrompt
	case "prefer_native", "native", "force_native":
		if upstream.NativeFC {
			return models.ResolvedNative
		}
		return models.ResolvedPrompt
	}
	switch globalMode {
	case "force_prompt":
		return models.ResolvedPrompt
	case "prefer_native", "auto":
		if upstream.NativeFC {
			return models.ResolvedNative
		}
		return models.ResolvedPrompt
	}
	if upstream.NativeFC {
		return models.ResolvedNative
	}
	return models.ResolvedPrompt
}

// ApplyPolicy sets req.FCMode and returns the request. Port of apply_policy.
func ApplyPolicy(req *models.CanonicalRequest, upstream config.UpstreamConfig, appConfig *config.AppConfig, headerOverride string) *models.CanonicalRequest {
	req.FCMode = ResolveFCMode(req, upstream, appConfig.Features, headerOverride)
	return req
}
