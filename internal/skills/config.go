// Package skills provides server-side skill management for request injection.
// Skills are loaded from disk and injected into API requests based on configuration.
//
// This package supports both our native format and the AgentSkills format
// (https://agentskills.io/specification) for compatibility with ClawHub and OpenClaw.
package skills

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// Config holds the skill configuration.
type Config struct {
	EnforcedLayers    []string       `yaml:"enforced-layers"`
	InjectedSkills    []string       `yaml:"injected-skills"`
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

// SkillMeta holds parsed frontmatter metadata for a skill.
// Supports both our native format (id, tags, type, priority) and
// AgentSkills format (name, description, metadata).
type SkillMeta struct {
	// ID is the skill identifier. We check both "id" (our format) and "name" (AgentSkills).
	ID string `yaml:"id"`
	// Name is the AgentSkills-compatible name field. Used as ID if id is empty.
	Name string `yaml:"name"`
	// Description of the skill.
	Description string `yaml:"description"`
	// Tags for categorization (our format).
	Tags []string `yaml:"tags"`
	// Type: requirement, pattern, or reference (our format).
	Type string `yaml:"type"`
	// Priority: must, should, may (our format).
	Priority string `yaml:"priority"`
	// License (AgentSkills format).
	License string `yaml:"license"`
	// Compatibility notes (AgentSkills format).
	Compatibility string `yaml:"compatibility"`
	// Metadata for arbitrary key-value pairs (AgentSkills format).
	Metadata map[string]interface{} `yaml:"metadata"`

	// Computed fields
	Layer    string // The layer this skill belongs to (e.g., "base", "profiles")
	FilePath string // Full path to the skill file
}

// GetID returns the effective ID, preferring "id" over "name".
func (m *SkillMeta) GetID() string {
	if m.ID != "" {
		return m.ID
	}
	return m.Name
}

// Manager handles skill loading and caching.
type Manager struct {
	mu         sync.RWMutex
	skillsDir  string
	config     *Config
	skillCache map[string]string     // layer/name -> content (body without frontmatter)
	skillMeta  map[string]*SkillMeta // layer/name -> parsed metadata
	skillByID  map[string]string     // skill ID -> cache key (for lookup by id/name)
	enabled    bool
}

// NewManager creates a new skill manager.
func NewManager(cfg SkillsConfig) (*Manager, error) {
	m := &Manager{
		skillsDir:  cfg.Dir,
		skillCache: make(map[string]string),
		skillMeta:  make(map[string]*SkillMeta),
		skillByID:  make(map[string]string),
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
// Skills are organized in layer directories (e.g., base/, personal/, profiles/)
// directly under the skills directory.
func (m *Manager) loadAllSkills() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Clear existing caches
	m.skillCache = make(map[string]string)
	m.skillMeta = make(map[string]*SkillMeta)
	m.skillByID = make(map[string]string)

	// Walk through skills directory looking for layer subdirectories
	entries, err := os.ReadDir(m.skillsDir)
	if err != nil {
		log.WithError(err).WithField("dir", m.skillsDir).Error("failed to read skills directory")
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		layer := entry.Name()
		// Skip hidden directories and config files
		if strings.HasPrefix(layer, ".") {
			continue
		}

		layerDir := filepath.Join(m.skillsDir, layer)
		if err := m.loadLayerSkills(layer, layerDir); err != nil {
			log.WithError(err).WithField("layer", layer).Warn("failed to load layer skills")
			continue
		}
	}

	log.WithFields(log.Fields{
		"skills_count": len(m.skillCache),
		"indexed_ids":  len(m.skillByID),
	}).Info("loaded skills into cache")
	return nil
}

// loadLayerSkills loads all skill files from a layer directory.
// Supports both AgentSkills format (dir/SKILL.md) and our format (dir/skill.md or file.md).
func (m *Manager) loadLayerSkills(layer, dir string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if info.IsDir() {
			return nil
		}

		filename := info.Name()
		lowerFilename := strings.ToLower(filename)

		// Only process markdown files
		if !strings.HasSuffix(lowerFilename, ".md") {
			return nil
		}

		// Extract name relative to layer directory
		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}

		// Determine the skill name/key
		var name string
		if lowerFilename == "skill.md" {
			// AgentSkills format: dir/SKILL.md or dir/skill.md
			// Use the directory name as the skill name
			parentDir := filepath.Dir(relPath)
			if parentDir == "." {
				// SKILL.md directly in layer dir - use layer name
				name = layer
			} else {
				// Use parent directory name
				name = strings.ReplaceAll(parentDir, string(filepath.Separator), "-")
			}
		} else {
			// Our format: file.md directly
			name = strings.TrimSuffix(relPath, ".md")
			name = strings.ReplaceAll(name, string(filepath.Separator), "-")
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		contentStr := string(content)
		key := layer + "/" + name

		// Parse frontmatter for metadata
		meta := m.parseFrontmatter(contentStr)
		meta.Layer = layer
		meta.FilePath = path

		// Store content (without frontmatter)
		m.skillCache[key] = stripFrontmatter(contentStr)
		m.skillMeta[key] = meta

		// Index by ID/name for lookup
		skillID := meta.GetID()
		if skillID != "" {
			m.skillByID[skillID] = key
		}
		// Also index by the path-derived name
		m.skillByID[name] = key

		log.WithFields(log.Fields{
			"key":      key,
			"skill_id": skillID,
			"size":     len(content),
		}).Debug("loaded skill")

		return nil
	})
}

// parseFrontmatter extracts and parses YAML frontmatter from content.
func (m *Manager) parseFrontmatter(content string) *SkillMeta {
	meta := &SkillMeta{}

	if !strings.HasPrefix(content, "---") {
		return meta
	}

	// Find the closing ---
	endIdx := strings.Index(content[3:], "\n---")
	if endIdx < 0 {
		return meta
	}

	// Extract frontmatter YAML
	frontmatter := content[4 : 3+endIdx]

	if err := yaml.Unmarshal([]byte(frontmatter), meta); err != nil {
		log.WithError(err).Debug("failed to parse skill frontmatter")
		return meta
	}

	return meta
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

// GetSkillByID returns the content of a skill by its ID or name.
// This looks up skills by their frontmatter id/name field.
func (m *Manager) GetSkillByID(id string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if key, ok := m.skillByID[id]; ok {
		if content, ok := m.skillCache[key]; ok {
			return content, true
		}
	}
	return "", false
}

// GetSkillMeta returns the metadata for a specific skill.
func (m *Manager) GetSkillMeta(layer, name string) (*SkillMeta, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := layer + "/" + name
	meta, ok := m.skillMeta[key]
	return meta, ok
}

// ListSkills returns all loaded skill metadata.
func (m *Manager) ListSkills() []*SkillMeta {
	m.mu.RLock()
	defer m.mu.RUnlock()

	skills := make([]*SkillMeta, 0, len(m.skillMeta))
	for _, meta := range m.skillMeta {
		skills = append(skills, meta)
	}
	return skills
}

// ListSkillsByTag returns skills matching any of the given tags.
func (m *Manager) ListSkillsByTag(tags []string) []*SkillMeta {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*SkillMeta
	for _, meta := range m.skillMeta {
		for _, wantTag := range tags {
			for _, hasTag := range meta.Tags {
				if matchTagPattern(wantTag, hasTag) {
					result = append(result, meta)
					goto nextSkill
				}
			}
		}
	nextSkill:
	}
	return result
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

// GetInjectedContent returns the combined content of only the explicitly listed
// injected skills. Unlike GetEnforcedContent which returns ALL skills in enforced
// layers, this returns only the specific skills listed in the injected-skills config.
// This supports the on-demand skill loading pattern where most skills are loaded
// via MCP tools rather than injected into every request.
func (m *Manager) GetInjectedContent() string {
	if !m.enabled || m.config == nil {
		return ""
	}

	// If no injected-skills configured, fall back to GetEnforcedContent for backward compat
	if len(m.config.InjectedSkills) == 0 {
		return m.GetEnforcedContent()
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var builder strings.Builder

	for _, skillID := range m.config.InjectedSkills {
		// First try the ID index (supports both id and name from frontmatter)
		if key, ok := m.skillByID[skillID]; ok {
			if content, ok := m.skillCache[key]; ok {
				if builder.Len() > 0 {
					builder.WriteString("\n\n")
				}
				builder.WriteString(content)
				continue
			}
		}

		// Fall back to searching by cache key or suffix
		found := false
		for key, content := range m.skillCache {
			// Match by exact key (e.g. "base/personality")
			// or by the name part after the last slash (e.g. "personality")
			name := key
			if idx := strings.LastIndex(key, "/"); idx >= 0 {
				name = key[idx+1:]
			}
			if key == skillID || name == skillID {
				if builder.Len() > 0 {
					builder.WriteString("\n\n")
				}
				builder.WriteString(content)
				found = true
				break
			}
		}
		if !found {
			log.WithField("skill", skillID).Warn("injected skill not found in cache")
		}
	}

	return builder.String()
}

// stripFrontmatter removes YAML frontmatter (delimited by --- lines) from content.
func stripFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---") {
		return content
	}

	// Find the closing ---
	endIdx := strings.Index(content[3:], "\n---")
	if endIdx < 0 {
		return content
	}

	// Skip past the closing --- and any trailing newline
	stripped := content[3+endIdx+4:]
	return strings.TrimLeft(stripped, "\n")
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

// matchTagPattern checks if a skill tag matches a wanted tag pattern.
// Supports wildcards like "writing/*" to match "writing/hemingway".
func matchTagPattern(pattern, tag string) bool {
	if pattern == tag {
		return true
	}

	// Support "domain/*" wildcard patterns
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(tag, prefix)
	}

	return false
}
