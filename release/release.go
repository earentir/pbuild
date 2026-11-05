// Package release uploads build artifacts to GitHub or Gitea/Forgejo releases.
package release

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	githubAPI    = "https://api.github.com"
	githubUpload = "https://uploads.github.com"
)

// Forge identifies the git hosting API.
type Forge string

const (
	ForgeGitHub Forge = "github"
	ForgeGitea  Forge = "gitea"
)

// ErrNotFound is returned when a release is not found (HTTP 404).
var ErrNotFound = fmt.Errorf("release not found")

// Config selects the hosting API.
type Config struct {
	Forge  Forge
	APIURL string // Gitea/Forgejo instance URL (e.g. https://git.example.com); ignored for GitHub
}

// Client performs release API calls for one forge.
type Client struct {
	cfg Config
}

// ParseForge normalizes forge type from profile/CLI ("github", "gitea", "forgejo").
func ParseForge(s string) Forge {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "gitea", "forgejo":
		return ForgeGitea
	default:
		return ForgeGitHub
	}
}

// NewClient validates config and returns a client.
func NewClient(cfg Config) (*Client, error) {
	cfg.Forge = ParseForge(string(cfg.Forge))
	if cfg.Forge == "" {
		cfg.Forge = ForgeGitHub
	}
	if cfg.Forge == ForgeGitea && strings.TrimSpace(cfg.APIURL) == "" {
		return nil, fmt.Errorf("gitea release upload requires api_url (Gitea instance base URL)")
	}
	return &Client{cfg: cfg}, nil
}

// TokenForForge reads GITEA_TOKEN or GITHUB_TOKEN for Gitea; GITHUB_TOKEN for GitHub.
func TokenForForge(forge Forge) (string, error) {
	switch ParseForge(string(forge)) {
	case ForgeGitea:
		if t := os.Getenv("GITEA_TOKEN"); t != "" {
			return t, nil
		}
		if t := os.Getenv("GITHUB_TOKEN"); t != "" {
			return t, nil
		}
		return "", fmt.Errorf("upload release: GITEA_TOKEN or GITHUB_TOKEN is required for Gitea")
	default:
		if t := os.Getenv("GITHUB_TOKEN"); t != "" {
			return t, nil
		}
		return "", fmt.Errorf("upload release: GITHUB_TOKEN is required")
	}
}

func (c *Client) apiBase() string {
	switch c.cfg.Forge {
	case ForgeGitea:
		return strings.TrimSuffix(strings.TrimSpace(c.cfg.APIURL), "/") + "/api/v1"
	default:
		return githubAPI
	}
}

func (c *Client) uploadBase() string {
	switch c.cfg.Forge {
	case ForgeGitea:
		return c.apiBase()
	default:
		return githubUpload
	}
}

// GetReleaseByTag fetches the release for the given tag.
func (c *Client) GetReleaseByTag(owner, repo, tag, token string) (releaseID int64, err error) {
	tag = url.PathEscape(tag)
	reqURL := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", c.apiBase(), owner, repo, tag)
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, err
	}
	c.setHeaders(req, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return 0, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("get release: %s %s", resp.Status, string(body))
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// CreateRelease creates a new release.
func (c *Client) CreateRelease(owner, repo, tagName, name, body string, draft bool, targetCommitish, token string) (releaseID int64, err error) {
	if targetCommitish == "" {
		targetCommitish = "HEAD"
	}
	if name == "" {
		name = tagName
	}
	payload := map[string]interface{}{
		"tag_name":         tagName,
		"name":             name,
		"body":             body,
		"draft":            draft,
		"target_commitish": targetCommitish,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	reqURL := fmt.Sprintf("%s/repos/%s/%s/releases", c.apiBase(), owner, repo)
	req, err := http.NewRequest(http.MethodPost, reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return 0, err
	}
	c.setHeaders(req, token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("create release: %s %s", resp.Status, string(respBody))
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// UploadAsset uploads a file as a release asset.
func (c *Client) UploadAsset(owner, repo string, releaseID int64, filePath, token string) error {
	if c.cfg.Forge == ForgeGitea {
		return c.uploadAssetGitea(owner, repo, releaseID, filePath, token)
	}
	return c.uploadAssetGitHub(owner, repo, releaseID, filePath, token)
}

func (c *Client) uploadAssetGitHub(owner, repo string, releaseID int64, filePath, token string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("cannot upload directory: %s", filePath)
	}
	name := filepath.Base(filePath)
	contentType := contentTypeFor(name)
	uploadURL := fmt.Sprintf("%s/repos/%s/%s/releases/%d/assets?name=%s", c.uploadBase(), owner, repo, releaseID, url.QueryEscape(name))
	req, err := http.NewRequest(http.MethodPost, uploadURL, f)
	if err != nil {
		return err
	}
	req.ContentLength = info.Size()
	c.setHeaders(req, token)
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload asset %s: %s %s", name, resp.Status, string(body))
	}
	return nil
}

func (c *Client) uploadAssetGitea(owner, repo string, releaseID int64, filePath, token string) error {
	info, err := os.Stat(filePath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("cannot upload directory: %s", filePath)
	}
	name := filepath.Base(filePath)
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("attachment", name)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, f); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	reqURL := fmt.Sprintf("%s/repos/%s/%s/releases/%d/assets?name=%s", c.apiBase(), owner, repo, releaseID, url.QueryEscape(name))
	req, err := http.NewRequest(http.MethodPost, reqURL, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	c.setHeaders(req, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload asset %s: %s %s", name, resp.Status, string(body))
	}
	return nil
}

func (c *Client) setHeaders(req *http.Request, token string) {
	switch c.cfg.Forge {
	case ForgeGitea:
		req.Header.Set("Accept", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "token "+token)
		}
	default:
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
}

func contentTypeFor(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".json", ".jsonl":
		return "application/json"
	case ".sig":
		return "application/octet-stream"
	case ".asc":
		return "text/plain"
	case ".txt", ".md":
		return "text/plain; charset=utf-8"
	default:
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}
		return "application/octet-stream"
	}
}
