package gitmeta

import (
	"errors"
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
// Supports https://github.com/owner/repo, https://github.com/owner/repo.git,
// and git@github.com:owner/repo.git. Returns empty strings and an error if
// origin is not set or URL cannot be parsed.
func RemoteOriginRepo(repoRoot string) (owner, repo string, err error) {
	cmd := exec.Command("git", "config", "remote.origin.url")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", "", err
	}
	url := strings.TrimSpace(string(out))
	if url == "" {
		return "", "", errors.New("remote origin URL is empty")
	}
	// https://github.com/owner/repo or https://github.com/owner/repo.git
	if strings.Contains(url, "github.com") {
		re := regexp.MustCompile(`github\.com[:/]([^/]+)/([^/]+?)(?:\.git)?$`)
		matches := re.FindStringSubmatch(url)
		if len(matches) == 3 {
			return matches[1], strings.TrimSuffix(matches[2], ".git"), nil
		}
	}
	// git@github.com:owner/repo.git
	if strings.HasPrefix(url, "git@github.com:") {
		trimmed := strings.TrimPrefix(url, "git@github.com:")
		trimmed = strings.TrimSuffix(trimmed, ".git")
		parts := strings.SplitN(trimmed, "/", 2)
		if len(parts) == 2 {
			return parts[0], parts[1], nil
		}
	}
	return "", "", errors.New("could not parse owner/repo from remote origin URL")
}
