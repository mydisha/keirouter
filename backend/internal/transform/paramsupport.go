package transform

import "strings"

// Param support rules strip request parameters a given model rejects upstream
// (HTTP 400). They are config-driven so a new quirk is one entry here rather
// than scattered deletes across codecs and connectors.

// modelRejectsTemperature reports whether a model rejects the temperature
// parameter. Anthropic's claude-opus-4 series deprecated it and returns a 400
// when it is present, regardless of which connector fronts the model.
func modelRejectsTemperature(model string) bool {
	return strings.Contains(strings.ToLower(model), "claude-opus-4") || modelRejectsSampling(model)
}

// samplingFreeModels are Claude families that accept no sampling overrides
// (temperature, top_p) at all.
var samplingFreeModels = []string{"claude-opus-5", "claude-sonnet-5", "claude-haiku-5", "claude-fable", "claude-mythos"}

// modelRejectsSampling reports whether a model rejects every sampling
// parameter, including top_p.
func modelRejectsSampling(model string) bool {
	m := strings.ToLower(model)
	for _, family := range samplingFreeModels {
		if strings.Contains(m, family) {
			return true
		}
	}
	return false
}
