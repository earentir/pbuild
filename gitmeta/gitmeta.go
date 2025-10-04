package gitmeta

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	head, err := os.Stat(filepath.Join(repoRoot, ".git", "HEAD"))
	if err != nil {
		return false, err
	}
	threshold := head.ModTime()
	dirty := false
	_ = filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			// Skip directories that typically contain build artifacts or generated files
			if name == ".git" || name == "vendor" || name == "builds" || name == "dist" || name == "bin" || name == "out" {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip common build artifacts and binaries
		name := d.Name()
		if name == "pbuild" || strings.HasSuffix(name, ".exe") || strings.HasSuffix(name, ".so") || strings.HasSuffix(name, ".dylib") || strings.HasSuffix(name, ".dll") {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if fi.ModTime().After(threshold.Add(1 * time.Second)) {
			dirty = true
			return errors.New("done")
		}
		return nil
	})
	return dirty, nil
}
