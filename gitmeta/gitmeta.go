package gitmeta

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
