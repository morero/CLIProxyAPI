package toon

import (
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// TOONHint is the explanation added to system prompts when TOON is enabled
const TOONHint = `Some structured data in this conversation uses TOON format for efficiency.
TOON syntax: arrays as 'name[count]{columns}:' with comma-separated rows, T/F for true/false, _ for null.
Example: users[2]{id,name}: followed by rows like '1,Alice' and '2,Bob'.
Respond in standard JSON unless instructed otherwise.`

// InjectTOONHint adds the TOON format explanation to the system prompt.
// This helps the LLM understand TOON-formatted data in the conversation.
func InjectTOONHint(body []byte) ([]byte, error) {
	// Check for system field (OpenAI Responses API style)
	if system := gjson.GetBytes(body, "system"); system.Exists() {
		newSystem := TOONHint + "\n\n" + system.String()
		return sjson.SetBytes(body, "system", newSystem)
	}

	// Check for system field as string (some formats)
	if instructions := gjson.GetBytes(body, "instructions"); instructions.Exists() {
		newInstructions := TOONHint + "\n\n" + instructions.String()
		return sjson.SetBytes(body, "instructions", newInstructions)
	}

	// Check for messages array - look for existing system message or prepend one
	messages := gjson.GetBytes(body, "messages")
	if messages.Exists() && messages.IsArray() {
		// Check if first message is system role
		firstMsg := messages.Array()
		if len(firstMsg) > 0 && firstMsg[0].Get("role").String() == "system" {
			// Prepend hint to existing system message
			existingContent := firstMsg[0].Get("content").String()
			newContent := TOONHint + "\n\n" + existingContent
			return sjson.SetBytes(body, "messages.0.content", newContent)
		}

		// No system message - prepend one
		// This is trickier - we need to insert at the beginning of the array
		var newMessages []interface{}
		newMessages = append(newMessages, map[string]interface{}{
			"role":    "system",
			"content": TOONHint,
		})

		// Add existing messages
		for _, msg := range firstMsg {
			newMessages = append(newMessages, msg.Value())
		}

		return sjson.SetBytes(body, "messages", newMessages)
	}

	// No system message capability found - return unchanged
	return body, nil
}

// HasTOONHint checks if the body already contains the TOON hint
func HasTOONHint(body []byte) bool {
	// Check system field
	if system := gjson.GetBytes(body, "system"); system.Exists() {
		if containsHint(system.String()) {
			return true
		}
	}

	// Check instructions field
	if instructions := gjson.GetBytes(body, "instructions"); instructions.Exists() {
		if containsHint(instructions.String()) {
			return true
		}
	}

	// Check first system message
	messages := gjson.GetBytes(body, "messages")
	if messages.Exists() && messages.IsArray() {
		firstMsg := messages.Array()
		if len(firstMsg) > 0 && firstMsg[0].Get("role").String() == "system" {
			if containsHint(firstMsg[0].Get("content").String()) {
				return true
			}
		}
	}

	return false
}

func containsHint(s string) bool {
	return len(s) >= 50 && (contains(s, "TOON format") || contains(s, "TOON syntax"))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr) >= 0
}

func findSubstring(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
