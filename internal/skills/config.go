// Package skills provides server-side skill management for request injection.
// Skills are loaded from disk and injected into API requests based on configuration.
package skills

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Config holds the skill configuration.
type Config struct {
	EnforcedLayers    []string       `yaml:"enforced-layers"`
	AvailableProfiles []string       `yaml:"available-profiles"`
	DefaultProfiles   DefaultProfile `yaml:"default-profiles"`
}

// DefaultProfile defines default profiles for requests.
type DefaultProfile struct {
	Global   []string            `yaml:"global"`
	ByAPIKey map[string][]string `yaml:"by-api-key"`
}

// SkillsConfig holds the main skills configuration from CLIProxyAPI config.
type SkillsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Dir     string `yaml:"dir"`
	Watch   bool   `yaml:"watch"`
}

// Manager handles skill loading and caching.
type Manager struct {
	mu         sync.RWMutex
	skillsDir  string
	config     *Config
	skillCache map[string]string // layer/name -> content
	enabled    bool
}

// NewManager creates a new skill manager.
func NewManager(cfg SkillsConfig) (*Manager, error) {
	m := &Manager{
		skillsDir:  cfg.Dir,
		skillCache: make(map[string]string),
		enabled:    cfg.Enabled,
	}

	if !cfg.Enabled {
		return m, nil
	}

	if cfg.Dir == "" {
		// Default to ../skills relative to working directory
		m.skillsDir = "../skills"
	}

	// Ensure directory exists
	if _, err := os.Stat(m.skillsDir); os.IsNotExist(err) {
		// Skills not configured, that's okay
		return m, nil
	}

	// Load configuration
	if err := m.loadConfig(); err != nil {
		return nil, err
	}

	// Load all skills into cache
	if err := m.loadAllSkills(); err != nil {
		return nil, err
	}

	return m, nil
}

// loadConfig loads the skill configuration from config.yaml.
func (m *Manager) loadConfig() error {
	configPath := filepath.Join(m.skillsDir, "config.yaml")

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No config, use defaults
			m.config = &Config{
				EnforcedLayers: []string{"base", "company"},
				DefaultProfiles: DefaultProfile{
					ByAPIKey: make(map[string][]string),
				},
			}
			return nil
		}
		return err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return err
	}

	if cfg.DefaultProfiles.ByAPIKey == nil {
		cfg.DefaultProfiles.ByAPIKey = make(map[string][]string)
	}

	m.config = &cfg
	return nil
}

// loadAllSkills loads all skill files into the cache.
func (m *Manager) loadAllSkills() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cacheDir := filepath.Join(m.skillsDir, "cache")
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		return nil
	}

	// Walk through cache directory
	return filepath.Walk(cacheDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}

		// Extract layer and name
		relPath, err := filepath.Rel(cacheDir, path)
		if err != nil {
			return nil
		}

		parts := strings.SplitN(relPath, string(filepath.Separator), 2)
		if len(parts) != 2 {
			return nil
		}

		layer := parts[0]
		name := strings.TrimSuffix(parts[1], ".md")
		name = strings.ReplaceAll(name, string(filepath.Separator), "-")

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		key := layer + "/" + name
		m.skillCache[key] = string(content)

		return nil
	})
}

// Reload reloads configuration and skills from disk.
func (m *Manager) Reload() error {
	if err := m.loadConfig(); err != nil {
		return err
	}
	return m.loadAllSkills()
}

// GetConfig returns the current configuration.
func (m *Manager) GetConfig() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config
}

// IsEnabled returns whether skills are enabled.
func (m *Manager) IsEnabled() bool {
	return m.enabled
}

// GetSkill returns the content of a specific skill.
func (m *Manager) GetSkill(layer, name string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := layer + "/" + name
	content, ok := m.skillCache[key]
	return content, ok
}

// GetLayerSkills returns all skills in a layer.
func (m *Manager) GetLayerSkills(layer string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var skills []string
	prefix := layer + "/"

	for key, content := range m.skillCache {
		if strings.HasPrefix(key, prefix) {
			skills = append(skills, content)
		}
	}

	return skills
}

// GetEnforcedContent returns the combined content of all enforced skills.
func (m *Manager) GetEnforcedContent() string {
	if !m.enabled || m.config == nil {
		return ""
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var builder strings.Builder

	for _, layer := range m.config.EnforcedLayers {
		prefix := layer + "/"
		for key, content := range m.skillCache {
			if strings.HasPrefix(key, prefix) {
				if builder.Len() > 0 {
					builder.WriteString("\n\n")
				}
				builder.WriteString(content)
			}
		}
	}

	return builder.String()
}

// GetProfileContent returns the combined content of specified profiles.
func (m *Manager) GetProfileContent(profiles []string) string {
	if !m.enabled || m.config == nil || len(profiles) == 0 {
		return ""
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var builder strings.Builder

	for _, profile := range profiles {
		// Check if profile is available
		available := false
		for _, p := range m.config.AvailableProfiles {
			if p == profile {
				available = true
				break
			}
		}
		if !available {
			continue
		}

		// Look for profile in profiles layer
		key := "profiles/" + profile
		if content, ok := m.skillCache[key]; ok {
			if builder.Len() > 0 {
				builder.WriteString("\n\n")
			}
			builder.WriteString(content)
		}
	}

	return builder.String()
}

// GetDefaultProfiles returns default profiles for an API key.
func (m *Manager) GetDefaultProfiles(apiKey string) []string {
	if !m.enabled || m.config == nil {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	// Check API key patterns
	for pattern, profiles := range m.config.DefaultProfiles.ByAPIKey {
		if matchPattern(pattern, apiKey) {
			return profiles
		}
	}

	// Fall back to global defaults
	return m.config.DefaultProfiles.Global
}

// matchPattern checks if a string matches a pattern with wildcards.
func matchPattern(pattern, s string) bool {
	if pattern == "*" {
		return true
	}

	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(s, prefix)
	}

	if strings.HasPrefix(pattern, "*") {
		suffix := strings.TrimPrefix(pattern, "*")
		return strings.HasSuffix(s, suffix)
	}

	return pattern == s
}
