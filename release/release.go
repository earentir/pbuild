// Package release provides GitHub release API helpers: get release by tag,
// create release, and upload assets via the GitHub REST API.
package release

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	apiHost    = "https://api.github.com"
	uploadHost = "https://uploads.github.com"
)

// ErrNotFound is returned when a release is not found (HTTP 404).
var ErrNotFound = fmt.Errorf("release not found")

// GetReleaseByTag fetches the release for the given tag. Returns the release ID
// or ErrNotFound if the release does not exist.
func GetReleaseByTag(owner, repo, tag, token string) (releaseID int64, err error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", apiHost, owner, repo, tag)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	setHeaders(req, token)
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

// CreateRelease creates a new release. tagName is required; name and body may be empty.
// targetCommitish (e.g. "HEAD" or "main") is used when the tag does not exist yet.
func CreateRelease(owner, repo, tagName, name, body string, draft bool, targetCommitish, token string) (releaseID int64, err error) {
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
	url := fmt.Sprintf("%s/repos/%s/%s/releases", apiHost, owner, repo)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return 0, err
	}
	setHeaders(req, token)
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

// UploadAsset uploads a file as a release asset. The asset name is the file's base name.
func UploadAsset(owner, repo string, releaseID int64, filePath, token string) error {
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
	uploadURL := fmt.Sprintf("%s/repos/%s/%s/releases/%d/assets?name=%s", uploadHost, owner, repo, releaseID, url.QueryEscape(name))
	req, err := http.NewRequest(http.MethodPost, uploadURL, f)
	if err != nil {
		return err
	}
	req.ContentLength = info.Size()
	setHeaders(req, token)
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

func setHeaders(req *http.Request, token string) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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
