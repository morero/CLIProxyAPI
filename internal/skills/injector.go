package skills

import (
	"encoding/json"
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Injector handles injecting skills into API requests.
type Injector struct {
	manager *Manager
}

// NewInjector creates a new skill injector.
func NewInjector(manager *Manager) *Injector {
	return &Injector{
		manager: manager,
	}
}

// InjectSkills injects enforced and profile skills into a request.
// apiKey is used to determine default profiles.
// requestedProfiles are profiles requested via X-Skill-Profiles header.
// skipEnforced can be set to true to skip enforced skills (for debugging).
func (i *Injector) InjectSkills(body []byte, apiKey string, requestedProfiles []string, skipEnforced bool) ([]byte, error) {
	if !i.manager.IsEnabled() {
		return body, nil
	}

	var skillContent strings.Builder

	// Add injected skills (unless skipped)
	if !skipEnforced {
		injected := i.manager.GetInjectedContent()
		if injected != "" {
			skillContent.WriteString(injected)
		}
	}

	// Determine profiles to apply
	profiles := requestedProfiles
	if len(profiles) == 0 {
		// Use default profiles for this API key
		profiles = i.manager.GetDefaultProfiles(apiKey)
	}

	// Handle special "none" profile that skips all profiles
	skipProfiles := false
	for _, p := range profiles {
		if p == "none" {
			skipProfiles = true
			break
		}
	}

	// Add profile skills
	if !skipProfiles && len(profiles) > 0 {
		profileContent := i.manager.GetProfileContent(profiles)
		if profileContent != "" {
			if skillContent.Len() > 0 {
				skillContent.WriteString("\n\n")
			}
			skillContent.WriteString("# Profile Skills\n\n")
			skillContent.WriteString(profileContent)
		}
	}

	if skillContent.Len() == 0 {
		log.Debug("no skill content to inject")
		return body, nil
	}

	log.WithField("content_length", skillContent.Len()).Debug("injecting skills into request")

	// Inject into the request
	return i.prependToSystemMessage(body, skillContent.String())
}

// prependToSystemMessage prepends content to the system message in a request.
func (i *Injector) prependToSystemMessage(body []byte, content string) ([]byte, error) {
	// Check if this is a chat completion request with messages
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() || !messages.IsArray() {
		// Try OpenAI responses format with input
		input := gjson.GetBytes(body, "input")
		if input.Exists() {
			return i.prependToInstructions(body, content)
		}
		return body, nil
	}

	// Detect Anthropic Messages API format: uses top-level "system" parameter
	// instead of a {"role": "system"} message. Anthropic format is identified by
	// having messages with only "user"/"assistant" roles and no "system" role messages,
	// combined with presence of anthropic-specific fields or absence of OpenAI-specific ones.
	if i.isAnthropicFormat(body, messages) {
		return i.prependToAnthropicSystem(body, content)
	}

	// OpenAI chat completions format: system role in messages array
	// Find existing system message
	var systemIdx = -1
	var currentIdx = 0
	messages.ForEach(func(_, value gjson.Result) bool {
		if value.Get("role").String() == "system" {
			systemIdx = currentIdx
			return false
		}
		currentIdx++
		return true
	})

	if systemIdx >= 0 {
		// Prepend to existing system message
		path := fmt.Sprintf("messages.%d.content", systemIdx)
		existingContent := gjson.GetBytes(body, path).String()
		newContent := content + "\n\n---\n\n" + existingContent
		return sjson.SetBytes(body, path, newContent)
	}

	// No system message, insert one at the beginning
	newMessage := map[string]string{
		"role":    "system",
		"content": content,
	}

	// Get current messages as array
	var msgArray []interface{}
	messages.ForEach(func(_, value gjson.Result) bool {
		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(value.Raw), &msg); err == nil {
			msgArray = append(msgArray, msg)
		}
		return true
	})

	// Prepend system message
	newMsgArray := append([]interface{}{newMessage}, msgArray...)

	return sjson.SetBytes(body, "messages", newMsgArray)
}

// isAnthropicFormat detects if the request body uses the Anthropic Messages API format.
// Anthropic format uses a top-level "system" param and does not allow "system" role in messages.
// Key indicators: "max_tokens" (required in Anthropic), top-level "system" field, or
// model name starting with "claude".
func (i *Injector) isAnthropicFormat(body []byte, messages gjson.Result) bool {
	// If there's already a top-level "system" field (string or array), it's Anthropic format
	system := gjson.GetBytes(body, "system")
	if system.Exists() {
		return true
	}

	// Check model name - only Claude models use Anthropic format
	model := strings.ToLower(gjson.GetBytes(body, "model").String())
	if strings.HasPrefix(model, "claude") {
		return true
	}

	// Explicitly NOT Anthropic format if model is from known OpenAI-compatible providers
	// These providers don't support top-level "system" field
	openAICompatPrefixes := []string{
		"gpt", "o1", "o3", "o4", // OpenAI
		"gemini", "imagen", // Google
		"llama", "mistral", "mixtral", "codestral", // Meta/Mistral
		"qwen", "coder", // Qwen
		"glm", "zai-glm", // Z.AI/Cerebras GLM
		"deepseek", // DeepSeek
		"grok",     // xAI
		"command",  // Cohere
	}
	for _, prefix := range openAICompatPrefixes {
		if strings.HasPrefix(model, prefix) {
			return false
		}
	}

	// Check if any message has "system" role - if so, it's OpenAI format
	hasSystemRole := false
	messages.ForEach(func(_, value gjson.Result) bool {
		if value.Get("role").String() == "system" {
			hasSystemRole = true
			return false
		}
		return true
	})
	if hasSystemRole {
		return false
	}

	// Only fall back to Anthropic detection heuristics for unknown models
	// If "max_tokens" is present without "max_completion_tokens" AND no system role,
	// it might be Anthropic, but only if model is truly unknown
	// Default to OpenAI format (safer - works with more providers)
	return false
}

// prependToAnthropicSystem prepends skill content to the Anthropic Messages API
// top-level "system" parameter. The system field can be a string or an array of
// content blocks.
func (i *Injector) prependToAnthropicSystem(body []byte, content string) ([]byte, error) {
	system := gjson.GetBytes(body, "system")

	if !system.Exists() {
		// No existing system param - add it as a string
		return sjson.SetBytes(body, "system", content)
	}

	if system.IsArray() {
		// System is an array of content blocks - prepend a text block
		newBlock := map[string]string{
			"type": "text",
			"text": content + "\n\n---\n\n",
		}
		var blocks []interface{}
		blocks = append(blocks, newBlock)
		system.ForEach(func(_, value gjson.Result) bool {
			var block map[string]interface{}
			if err := json.Unmarshal([]byte(value.Raw), &block); err == nil {
				blocks = append(blocks, block)
			}
			return true
		})
		return sjson.SetBytes(body, "system", blocks)
	}

	// System is a string - prepend to it
	existing := system.String()
	newSystem := content + "\n\n---\n\n" + existing
	return sjson.SetBytes(body, "system", newSystem)
}

// prependToInstructions handles OpenAI responses API format.
func (i *Injector) prependToInstructions(body []byte, content string) ([]byte, error) {
	instructions := gjson.GetBytes(body, "instructions")
	if instructions.Exists() {
		newInstructions := content + "\n\n---\n\n" + instructions.String()
		return sjson.SetBytes(body, "instructions", newInstructions)
	}

	// Add instructions field
	return sjson.SetBytes(body, "instructions", content)
}

// ParseProfilesHeader parses the X-Skill-Profiles header value.
func ParseProfilesHeader(header string) []string {
	if header == "" {
		return nil
	}

	var profiles []string
	for _, p := range strings.Split(header, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			profiles = append(profiles, p)
		}
	}
	return profiles
}
