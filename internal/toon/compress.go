package toon

import (
	"bytes"
	"regexp"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// CompressToolResults compresses JSON tool results in a message payload.
// It looks for tool_result content blocks and compresses their JSON content.
func CompressToolResults(body []byte, opts Options) ([]byte, bool) {
	if !gjson.ValidBytes(body) {
		return body, false
	}

	anyCompressed := false

	// Check for messages array (chat completions format)
	messages := gjson.GetBytes(body, "messages")
	if messages.Exists() && messages.IsArray() {
		newBody := body
		messages.ForEach(func(msgIdx, msg gjson.Result) bool {
			// Check for tool results in content
			content := msg.Get("content")
			if content.IsArray() {
				content.ForEach(func(blockIdx, block gjson.Result) bool {
					if block.Get("type").String() == "tool_result" {
						// Get the tool result content
						toolContent := block.Get("content")
						if toolContent.Exists() {
							compressed, wasCompressed := compressJSONString(toolContent.String(), opts)
							if wasCompressed {
								path := "messages." + itoa(int(msgIdx.Int())) + ".content." + itoa(int(blockIdx.Int())) + ".content"
								newBody, _ = sjson.SetBytes(newBody, path, compressed)
								anyCompressed = true
							}
						}
					}
					return true
				})
			}
			return true
		})
		body = newBody
	}

	return body, anyCompressed
}

// CompressJSONInContent finds and compresses JSON blocks in text content.
// Looks for JSON objects/arrays and replaces them with TOON format.
func CompressJSONInContent(content string, opts Options) (string, bool) {
	// Find JSON blocks in content
	jsonPattern := regexp.MustCompile(`(?s)(\{[^{}]*(?:\{[^{}]*\}[^{}]*)*\}|\[[^\[\]]*(?:\[[^\[\]]*\][^\[\]]*)*\])`)

	anyCompressed := false
	result := jsonPattern.ReplaceAllStringFunc(content, func(match string) string {
		compressed, wasCompressed := compressJSONString(match, opts)
		if wasCompressed {
			anyCompressed = true
			return compressed
		}
		return match
	})

	return result, anyCompressed
}

// compressJSONString attempts to compress a JSON string to TOON format.
func compressJSONString(jsonStr string, opts Options) (string, bool) {
	jsonBytes := []byte(jsonStr)

	// Skip if too small
	if len(jsonBytes) < opts.MinSizeBytes {
		return jsonStr, false
	}

	compressed, wasCompressed, err := Encode(jsonBytes, opts)
	if err != nil || !wasCompressed {
		return jsonStr, false
	}

	return string(compressed), true
}

// CompressMessages compresses JSON content in message bodies.
// Processes user, assistant, and system messages.
func CompressMessages(body []byte, opts Options) ([]byte, bool) {
	if !gjson.ValidBytes(body) {
		return body, false
	}

	anyCompressed := false
	newBody := body

	// Process messages array
	messages := gjson.GetBytes(body, "messages")
	if messages.Exists() && messages.IsArray() {
		messages.ForEach(func(idx, msg gjson.Result) bool {
			content := msg.Get("content")

			// Handle string content
			if content.Type == gjson.String {
				compressed, wasCompressed := CompressJSONInContent(content.String(), opts)
				if wasCompressed {
					path := "messages." + itoa(int(idx.Int())) + ".content"
					newBody, _ = sjson.SetBytes(newBody, path, compressed)
					anyCompressed = true
				}
			}

			return true
		})
	}

	return newBody, anyCompressed
}

// CompressSystemPrompt compresses JSON blocks in the system prompt.
func CompressSystemPrompt(body []byte, opts Options) ([]byte, bool) {
	if !gjson.ValidBytes(body) {
		return body, false
	}

	// Check for system field (OpenAI Responses API style)
	system := gjson.GetBytes(body, "system")
	if system.Exists() && system.Type == gjson.String {
		compressed, wasCompressed := CompressJSONInContent(system.String(), opts)
		if wasCompressed {
			newBody, _ := sjson.SetBytes(body, "system", compressed)
			return newBody, true
		}
	}

	// Check for system message in messages array
	messages := gjson.GetBytes(body, "messages")
	if messages.Exists() && messages.IsArray() {
		newBody := body
		anyCompressed := false

		messages.ForEach(func(idx, msg gjson.Result) bool {
			if msg.Get("role").String() == "system" {
				content := msg.Get("content")
				if content.Type == gjson.String {
					compressed, wasCompressed := CompressJSONInContent(content.String(), opts)
					if wasCompressed {
						path := "messages." + itoa(int(idx.Int())) + ".content"
						newBody, _ = sjson.SetBytes(newBody, path, compressed)
						anyCompressed = true
					}
				}
			}
			return true
		})

		if anyCompressed {
			return newBody, true
		}
	}

	return body, false
}

// ApplyTOONCompression applies TOON compression to a request body.
// Compresses tool results, system prompts, and message content.
func ApplyTOONCompression(body []byte, opts Options) ([]byte, Stats) {
	originalSize := len(body)
	totalStats := Stats{OriginalBytes: originalSize}

	// Compress tool results first (highest value)
	body, compressed1 := CompressToolResults(body, opts)

	// Compress system prompts
	body, compressed2 := CompressSystemPrompt(body, opts)

	// Compress message content
	body, compressed3 := CompressMessages(body, opts)

	totalStats.CompressedBytes = len(body)
	totalStats.WasCompressed = compressed1 || compressed2 || compressed3

	if totalStats.WasCompressed && originalSize > 0 {
		totalStats.SavingsPercent = float64(originalSize-len(body)) / float64(originalSize) * 100
	}

	return body, totalStats
}

// itoa is a simple int to string conversion
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf bytes.Buffer
	for i > 0 {
		buf.WriteByte(byte('0' + i%10))
		i /= 10
	}
	// Reverse
	b := buf.Bytes()
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}
