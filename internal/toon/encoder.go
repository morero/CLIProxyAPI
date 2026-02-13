// Package toon provides TOON (Token-Oriented Object Notation) encoding
// for reducing token usage when communicating with LLMs.
package toon

import (
	"encoding/json"

	"github.com/roboogg133/goon/goon"
)

// Options configures TOON encoding behavior
type Options struct {
	// MinSizeBytes is the minimum size of JSON content to compress (default: 200)
	MinSizeBytes int
	// WrapWithTags wraps output in <toon>...</toon> tags for LLM clarity
	WrapWithTags bool
}

// DefaultOptions returns sensible defaults for TOON encoding
func DefaultOptions() Options {
	return Options{
		MinSizeBytes: 200,
		WrapWithTags: true,
	}
}

// Encode converts JSON bytes to TOON format.
// Returns original bytes if compression is not beneficial or content is too small.
func Encode(jsonBytes []byte, opts Options) ([]byte, bool, error) {
	if opts.MinSizeBytes == 0 {
		opts.MinSizeBytes = 200
	}

	// Skip if content is too small
	if len(jsonBytes) < opts.MinSizeBytes {
		return jsonBytes, false, nil
	}

	// Validate JSON
	if !json.Valid(jsonBytes) {
		return jsonBytes, false, nil
	}

	// Parse JSON into interface{}
	var data interface{}
	if err := json.Unmarshal(jsonBytes, &data); err != nil {
		return jsonBytes, false, nil
	}

	// Check if data is compressible (arrays of objects work best)
	if !isCompressible(data) {
		return jsonBytes, false, nil
	}

	// Encode to TOON
	toonBytes, err := goon.Marshal(data)
	if err != nil {
		return jsonBytes, false, err
	}

	// Only use TOON if it's actually smaller
	if len(toonBytes) >= len(jsonBytes) {
		return jsonBytes, false, nil
	}

	// Wrap with tags if requested
	if opts.WrapWithTags {
		toonBytes = wrapWithTags(toonBytes)
	}

	return toonBytes, true, nil
}

// EncodeAny converts any Go value to TOON format.
func EncodeAny(data interface{}, opts Options) ([]byte, error) {
	return goon.Marshal(data)
}

// isCompressible checks if the data structure would benefit from TOON encoding.
// TOON works best with arrays of objects with uniform keys.
func isCompressible(data interface{}) bool {
	switch v := data.(type) {
	case []interface{}:
		// Arrays are good candidates, especially arrays of objects
		if len(v) >= 2 {
			// Check if it's an array of objects with same keys
			return isUniformObjectArray(v)
		}
		return false
	case map[string]interface{}:
		// Objects with array values are good candidates
		for _, val := range v {
			if arr, ok := val.([]interface{}); ok && len(arr) >= 2 {
				if isUniformObjectArray(arr) {
					return true
				}
			}
		}
		// Nested objects can also benefit
		return len(v) >= 3
	default:
		return false
	}
}

// isUniformObjectArray checks if all elements are objects with the same keys
func isUniformObjectArray(arr []interface{}) bool {
	if len(arr) < 2 {
		return false
	}

	// Get keys from first object
	firstObj, ok := arr[0].(map[string]interface{})
	if !ok {
		return false
	}

	firstKeys := make(map[string]bool)
	for k := range firstObj {
		firstKeys[k] = true
	}

	// Check remaining objects have same keys
	for i := 1; i < len(arr); i++ {
		obj, ok := arr[i].(map[string]interface{})
		if !ok {
			return false
		}
		if len(obj) != len(firstKeys) {
			return false
		}
		for k := range obj {
			if !firstKeys[k] {
				return false
			}
		}
	}

	return true
}

// wrapWithTags wraps TOON content with <toon> tags for LLM clarity
func wrapWithTags(toonBytes []byte) []byte {
	result := make([]byte, 0, len(toonBytes)+15)
	result = append(result, "<toon>\n"...)
	result = append(result, toonBytes...)
	if len(toonBytes) > 0 && toonBytes[len(toonBytes)-1] != '\n' {
		result = append(result, '\n')
	}
	result = append(result, "</toon>"...)
	return result
}

// Stats contains compression statistics
type Stats struct {
	OriginalBytes   int
	CompressedBytes int
	SavingsPercent  float64
	WasCompressed   bool
}

// GetStats calculates compression statistics
func GetStats(original, compressed []byte, wasCompressed bool) Stats {
	stats := Stats{
		OriginalBytes:   len(original),
		CompressedBytes: len(compressed),
		WasCompressed:   wasCompressed,
	}
	if wasCompressed && stats.OriginalBytes > 0 {
		stats.SavingsPercent = float64(stats.OriginalBytes-stats.CompressedBytes) / float64(stats.OriginalBytes) * 100
	}
	return stats
}
