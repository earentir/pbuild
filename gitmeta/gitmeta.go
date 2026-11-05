package gitmeta

import (
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func ResolveHEAD(repoRoot string) (string, error) {
	gitDir := filepath.Join(repoRoot, ".git")
	b, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(b))
	if strings.HasPrefix(line, "ref:") {
		ref := strings.TrimSpace(strings.TrimPrefix(line, "ref:"))
		refPath := filepath.Join(gitDir, ref)
		if bb, err := os.ReadFile(refPath); err == nil {
			rev := strings.TrimSpace(string(bb))
			if len(rev) >= 7 {
				return rev[:7], nil
			}
			return rev, nil
		}
		if pb, err := os.ReadFile(filepath.Join(gitDir, "packed-refs")); err == nil {
			for _, l := range strings.Split(string(pb), "\n") {
				l = strings.TrimSpace(l)
				if l == "" || strings.HasPrefix(l, "#") {
					continue
				}
				parts := strings.Fields(l)
				if len(parts) == 2 && parts[1] == ref {
					rev := parts[0]
					if len(rev) >= 7 {
						return rev[:7], nil
					}
					return rev, nil
				}
			}
		}
		return "", errors.New("ref not found")
	}
	rev := line
	if len(rev) >= 7 {
		return rev[:7], nil
	}
	return rev, nil
}

func HeuristicDirty(repoRoot string) (bool, error) {
	// Check if there are local changes (uncommitted files)
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoRoot
	output, err := cmd.Output()
	if err != nil {
		return false, nil
	}

	// If there are local changes, repo is dirty
	if len(strings.TrimSpace(string(output))) > 0 {
		return true, nil
	}

	// Check if local repo is behind remote
	cmd = exec.Command("git", "fetch", "--dry-run")
	cmd.Dir = repoRoot
	err = cmd.Run()
	if err != nil {
		// If remote is not accessible, consider it clean
		return false, nil
	}

	// Check if local branch is behind remote
	cmd = exec.Command("git", "status", "-uno")
	cmd.Dir = repoRoot
	output, err = cmd.Output()
	if err != nil {
		return false, nil
	}

	// If output contains "behind", repo is dirty (not in sync with remote)
	return strings.Contains(string(output), "behind"), nil
}

// RemoteOriginRepo returns owner and repo name from git remote origin URL.
// Supports GitHub, Gitea, Forgejo, and other hosts using common URL layouts:
// https://host/owner/repo(.git), git@host:owner/repo(.git), and github.com variants.
func RemoteOriginRepo(repoRoot string) (owner, repo string, err error) {
	cmd := exec.Command("git", "config", "remote.origin.url")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", "", err
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return "", "", errors.New("remote origin URL is empty")
	}
	if o, r, ok := parseOwnerRepoFromRemoteURL(raw); ok {
		return o, r, nil
	}
	return "", "", errors.New("could not parse owner/repo from remote origin URL")
}

func parseOwnerRepoFromRemoteURL(raw string) (owner, repo string, ok bool) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, ".git")

	// git@host:owner/repo
	if strings.HasPrefix(raw, "git@") && strings.Contains(raw, ":") {
		idx := strings.Index(raw, ":")
		path := raw[idx+1:]
		parts := strings.SplitN(path, "/", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return parts[0], parts[1], true
		}
	}

	// https://host/owner/repo or https://host/owner/repo.git
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", "", false
		}
		segments := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(segments) >= 2 {
			return segments[len(segments)-2], segments[len(segments)-1], true
		}
	}

	// github.com shorthand patterns
	if strings.Contains(raw, "github.com") {
		re := regexp.MustCompile(`github\.com[:/]([^/]+)/([^/]+?)$`)
		matches := re.FindStringSubmatch(raw)
		if len(matches) == 3 {
			return matches[1], matches[2], true
		}
	}
	return "", "", false
}
