package appver

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// Strict: var appVersion = "..."
	reVarAppVersion = regexp.MustCompile(`var\s+appVersion\s*=\s*"([^"]+)"`)
	// Optional: const/var appVersion (identifier only), case-insensitive — not generic "Version".
	reConstVarAppVersion = regexp.MustCompile(`(?i)(?:var|const)\s+appVersion\s*=\s*"([^"]+)"`)
)

func extractAppVersionFromSource(b []byte) (string, bool) {
	if m := reVarAppVersion.FindSubmatch(b); len(m) == 2 {
		return string(m[1]), true
	}
	if m := reConstVarAppVersion.FindSubmatch(b); len(m) == 2 {
		return string(m[1]), true
	}
	return "", false
}

// ExtractAppVersion finds var appVersion = "..." (or const/var appVersion, case-insensitive)
// in .go files under root. The previous implementation also used a loose regex that matched
// any assignment to an identifier ending in "version", e.g. const Version = "1", which could
// win before main.go was read and produced base versions like "1" instead of "1.4.175".
func ExtractAppVersion(root string) (string, error) {
	var found string
	walk := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "vendor" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if v, ok := extractAppVersionFromSource(b); ok {
			found = v
			return errors.New("done")
		}
		return nil
	}
	_ = filepath.WalkDir(root, walk)
	if found == "" {
		return "", errors.New("version not found")
	}
	return found, nil
}
