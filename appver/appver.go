package appver

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var re = regexp.MustCompile(`var\s+appVersion\s*=\s*"([^"]+)"`)

func ExtractAppVersion(root string) (string, error) {
	// Fallback patterns: case-insensitive, handle var/const, optional type, and var blocks.
	reList := []*regexp.Regexp{
		regexp.MustCompile(`(?is)\b(appversion|version)\b[^\n=]*=\s*"([^"]+)"`),
	}

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

		// Try original case-sensitive regex to keep package var `re` in use.
		if m := re.FindSubmatch(b); len(m) == 2 {
			found = string(m[1])
			return errors.New("done")
		}

		// Try broader, case-insensitive patterns.
		for _, rx := range reList {
			if m := rx.FindSubmatch(b); len(m) == 3 {
				found = string(m[2])
				return errors.New("done")
			}
		}
		return nil
	}
	_ = filepath.WalkDir(root, walk)
	if found == "" {
		return "", errors.New("version not found")
	}
	return found, nil
}
