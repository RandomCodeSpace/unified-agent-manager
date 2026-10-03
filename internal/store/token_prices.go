package store

import (
	"math"
	"strings"
	"unicode"
)

// ValidTokenPriceModel accepts a model ID or the full selection ID of a custom model.
func ValidTokenPriceModel(model string) bool {
	if ValidHiddenModel(model) {
		return true
	}
	name, id, found := strings.Cut(model, "/")
	return found && len(name) <= MaxCustomModelNameBytes && customModelName.MatchString(name) &&
		ValidHiddenModel(id) && !strings.ContainsFunc(id, unicode.IsSpace)
}

// WebTokenPrice is an estimate in USD per million tokens. Input and output
// are required, including explicit zero prices. Cache rates are optional;
// absent cache rates use the input price.
type WebTokenPrice struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read,omitempty"`
	CacheWrite *float64 `json:"cache_write,omitempty"`
}

func (p WebTokenPrice) Valid() bool {
	if p.Input == nil || p.Output == nil {
		return false
	}
	for _, v := range []*float64{p.Input, p.Output, p.CacheRead, p.CacheWrite} {
		if v != nil && (*v < 0 || *v > 1e9 || math.IsNaN(*v) || math.IsInf(*v, 0)) {
			return false
		}
	}
	return true
}
