// Package skills provides ClawHub integration for browsing and searching
// community skills from the public registry at clawhub.com.
package skills

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	// DefaultClawHubURL is the default ClawHub API base URL.
	DefaultClawHubURL = "https://clawhub.com/api/v1"
)

// ClawHubClient provides access to the ClawHub public API.
type ClawHubClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewClawHubClient creates a new ClawHub client.
func NewClawHubClient() *ClawHubClient {
	return &ClawHubClient{
		baseURL: DefaultClawHubURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ClawHubSkill represents a skill from ClawHub.
type ClawHubSkill struct {
	Slug        string            `json:"slug"`
	DisplayName string            `json:"displayName"`
	Summary     string            `json:"summary"`
	Tags        map[string]string `json:"tags"` // tag name -> version
	Stats       ClawHubStats      `json:"stats"`
	CreatedAt   int64             `json:"createdAt"`
	UpdatedAt   int64             `json:"updatedAt"`
}

// ClawHubStats contains skill statistics.
type ClawHubStats struct {
	Comments        int `json:"comments"`
	Downloads       int `json:"downloads"`
	InstallsAllTime int `json:"installsAllTime"`
	InstallsCurrent int `json:"installsCurrent"`
	Stars           int `json:"stars"`
	Versions        int `json:"versions"`
}

// ClawHubSkillDetail contains full skill details including owner.
type ClawHubSkillDetail struct {
	Skill         ClawHubSkill   `json:"skill"`
	LatestVersion ClawHubVersion `json:"latestVersion"`
	Owner         ClawHubOwner   `json:"owner"`
}

// ClawHubVersion contains version information.
type ClawHubVersion struct {
	Version   string `json:"version"`
	CreatedAt int64  `json:"createdAt"`
	Changelog string `json:"changelog"`
}

// ClawHubOwner contains skill owner information.
type ClawHubOwner struct {
	Handle      string `json:"handle"`
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName"`
	Image       string `json:"image"`
}

// ClawHubVersionDetail contains full version details including files.
type ClawHubVersionDetail struct {
	Skill   ClawHubSkillRef   `json:"skill"`
	Version ClawHubVersionRef `json:"version"`
}

// ClawHubSkillRef is a minimal skill reference.
type ClawHubSkillRef struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"displayName"`
}

// ClawHubVersionRef contains version details with file list.
type ClawHubVersionRef struct {
	Version         string        `json:"version"`
	CreatedAt       int64         `json:"createdAt"`
	Changelog       string        `json:"changelog"`
	ChangelogSource string        `json:"changelogSource"`
	Files           []ClawHubFile `json:"files"`
}

// ClawHubFile represents a file in a skill version.
type ClawHubFile struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	ContentType string `json:"contentType"`
}

// ClawHubSearchResult represents a search result.
type ClawHubSearchResult struct {
	Score       float64 `json:"score"`
	Slug        string  `json:"slug"`
	DisplayName string  `json:"displayName"`
	Summary     string  `json:"summary"`
	Version     string  `json:"version"`
	UpdatedAt   int64   `json:"updatedAt"`
}

// ListSkillsResponse is the response from listing skills.
type ListSkillsResponse struct {
	Items      []ClawHubSkill `json:"items"`
	NextCursor string         `json:"nextCursor"`
}

// SearchResponse is the response from searching skills.
type SearchResponse struct {
	Results []ClawHubSearchResult `json:"results"`
}

// ListSkills lists skills from ClawHub with optional pagination.
func (c *ClawHubClient) ListSkills(limit int, cursor string) (*ListSkillsResponse, error) {
	u, err := url.Parse(c.baseURL + "/skills")
	if err != nil {
		return nil, err
	}

	q := u.Query()
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	u.RawQuery = q.Encode()

	resp, err := c.httpClient.Get(u.String())
	if err != nil {
		return nil, fmt.Errorf("failed to list skills: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ClawHub API error: %s (status %d)", string(body), resp.StatusCode)
	}

	var result ListSkillsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// GetSkill gets details for a specific skill.
func (c *ClawHubClient) GetSkill(slug string) (*ClawHubSkillDetail, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/skills/" + url.PathEscape(slug))
	if err != nil {
		return nil, fmt.Errorf("failed to get skill: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("skill not found: %s", slug)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ClawHub API error: %s (status %d)", string(body), resp.StatusCode)
	}

	var result ClawHubSkillDetail
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// GetVersion gets details for a specific version of a skill.
func (c *ClawHubClient) GetVersion(slug, version string) (*ClawHubVersionDetail, error) {
	path := fmt.Sprintf("/skills/%s/versions/%s", url.PathEscape(slug), url.PathEscape(version))
	resp, err := c.httpClient.Get(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("failed to get version: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("version not found: %s@%s", slug, version)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ClawHub API error: %s (status %d)", string(body), resp.StatusCode)
	}

	var result ClawHubVersionDetail
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// Search searches for skills using semantic search.
func (c *ClawHubClient) Search(query string, limit int) (*SearchResponse, error) {
	u, err := url.Parse(c.baseURL + "/search")
	if err != nil {
		return nil, err
	}

	q := u.Query()
	q.Set("q", query)
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	u.RawQuery = q.Encode()

	resp, err := c.httpClient.Get(u.String())
	if err != nil {
		return nil, fmt.Errorf("failed to search skills: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ClawHub API error: %s (status %d)", string(body), resp.StatusCode)
	}

	var result SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}
