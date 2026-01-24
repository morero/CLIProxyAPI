package skills

import (
	"encoding/json"
	"fmt"
	"strings"

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

	// Add enforced skills (unless skipped)
	if !skipEnforced {
		enforced := i.manager.GetEnforcedContent()
		if enforced != "" {
			skillContent.WriteString("# Enforced Skills\n\n")
			skillContent.WriteString(enforced)
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
		return body, nil
	}

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
