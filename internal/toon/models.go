package toon

import "strings"

// TOONModelAliases maps TOON model aliases to their base models
var TOONModelAliases = map[string]string{
	// Claude
	"claude-opus-toon":   "claude-opus-4-6",
	"claude-sonnet-toon": "claude-sonnet-4-5-20250929",
	"claude-haiku-toon":  "claude-haiku-4-5-20251001",

	// OpenAI/Codex
	"gpt-toon":       "gpt-5.2",
	"gpt-codex-toon": "gpt-5.2-codex",

	// Gemini
	"gemini-pro-toon":   "gemini-2.5-pro",
	"gemini-flash-toon": "gemini-2.5-flash",

	// xAI/Grok
	"grok-toon":      "grok-4-1-fast-reasoning",
	"grok-code-toon": "grok-code-fast-1",
}

// ModelInfo contains parsed TOON model information
type ModelInfo struct {
	// BaseModel is the underlying model to use (e.g., "claude-opus-4-6")
	BaseModel string
	// UseTOON indicates whether TOON compression should be applied
	UseTOON bool
	// Use1MContext indicates whether 1M context beta should be enabled
	Use1MContext bool
}

// ParseModel parses a model name and extracts TOON and 1M context flags.
// Supports both suffix-based models (any-model-toon) and alias models (claude-opus-toon).
// Also handles ordering of -toon and -1m suffixes in any order.
func ParseModel(model string) ModelInfo {
	info := ModelInfo{BaseModel: model}

	// Check for -toon suffix (can be combined with -1m in any order)
	// Handle: model-toon, model-1m-toon, model-toon-1m
	if strings.HasSuffix(model, "-toon") {
		info.UseTOON = true
		model = strings.TrimSuffix(model, "-toon")
	}

	if strings.HasSuffix(model, "-1m") {
		info.Use1MContext = true
		model = strings.TrimSuffix(model, "-1m")
	}

	// Check again for -toon in case it was before -1m (model-toon-1m)
	if strings.HasSuffix(model, "-toon") {
		info.UseTOON = true
		model = strings.TrimSuffix(model, "-toon")
	}

	// Check if this is a TOON alias that needs resolution
	if baseModel, ok := TOONModelAliases[info.BaseModel]; ok {
		info.BaseModel = baseModel
		info.UseTOON = true
	} else {
		info.BaseModel = model
	}

	return info
}

// IsTOONModel checks if a model name indicates TOON compression should be used
func IsTOONModel(model string) bool {
	// Check for -toon suffix
	if strings.Contains(model, "-toon") {
		return true
	}

	// Check for TOON aliases
	_, isAlias := TOONModelAliases[model]
	return isAlias
}

// ResolveBaseModel returns the base model name for a given TOON model.
// If not a TOON alias, returns the original model name with -toon/-1m suffixes stripped.
func ResolveBaseModel(model string) string {
	return ParseModel(model).BaseModel
}
