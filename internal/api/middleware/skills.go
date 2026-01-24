package middleware

import (
	"bytes"
	"io"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/skills"
	log "github.com/sirupsen/logrus"
)

const (
	// HeaderSkillProfiles is the header for requesting specific skill profiles.
	HeaderSkillProfiles = "X-Skill-Profiles"
	// HeaderSkipEnforcedSkills is the header to skip enforced skills (requires admin API key).
	HeaderSkipEnforcedSkills = "X-Skip-Enforced-Skills"
)

// SkillInjectionMiddleware creates middleware that injects skills into requests.
func SkillInjectionMiddleware(injector *skills.Injector, isAdminKeyFunc func(string) bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if injector == nil {
			c.Next()
			return
		}

		// Only process POST requests with JSON body (API calls)
		if c.Request.Method != "POST" {
			c.Next()
			return
		}

		contentType := c.GetHeader("Content-Type")
		if contentType != "" &&
			!bytes.Contains([]byte(contentType), []byte("application/json")) {
			c.Next()
			return
		}

		// Skip non-API paths
		path := c.Request.URL.Path
		if !isAPIPath(path) {
			c.Next()
			return
		}

		// Read body
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.Next()
			return
		}
		c.Request.Body.Close()

		// Skip empty bodies
		if len(body) == 0 {
			c.Request.Body = io.NopCloser(bytes.NewReader(body))
			c.Next()
			return
		}

		// Get API key from context or header
		apiKey := c.GetString("api_key")
		if apiKey == "" {
			apiKey = c.GetHeader("Authorization")
			if len(apiKey) > 7 && apiKey[:7] == "Bearer " {
				apiKey = apiKey[7:]
			}
		}

		// Parse profile header
		profileHeader := c.GetHeader(HeaderSkillProfiles)
		profiles := skills.ParseProfilesHeader(profileHeader)

		// Check if enforced skills should be skipped
		skipEnforced := false
		if c.GetHeader(HeaderSkipEnforcedSkills) == "true" {
			// Only allow skipping for admin keys
			if isAdminKeyFunc != nil && isAdminKeyFunc(apiKey) {
				skipEnforced = true
				log.Debug("skipping enforced skills for admin key")
			}
		}

		// Inject skills
		modifiedBody, err := injector.InjectSkills(body, apiKey, profiles, skipEnforced)
		if err != nil {
			log.WithError(err).Warn("failed to inject skills")
			modifiedBody = body // Use original on error
		}

		// Replace body
		c.Request.Body = io.NopCloser(bytes.NewReader(modifiedBody))
		c.Request.ContentLength = int64(len(modifiedBody))

		c.Next()
	}
}

// isAPIPath checks if a path should have skills injected.
func isAPIPath(path string) bool {
	// API paths that accept chat/completion requests
	apiPaths := []string{
		"/v1/chat/completions",
		"/v1/responses",
		"/v1/messages",
		"/api/provider/",
	}

	for _, prefix := range apiPaths {
		if bytes.HasPrefix([]byte(path), []byte(prefix)) {
			return true
		}
	}

	return false
}
