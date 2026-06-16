package appver

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Result holds a discovered appversion assignment.
type Result struct {
	Version string
	File    string
}

// reAppVersionAssignment matches appversion = "..." in code (case-insensitive identifier).
var reAppVersionAssignment = regexp.MustCompile(`(?i)\bappversion\b\s*=\s*"([^"]*)"`)
var reLineComment = regexp.MustCompile(`//.*`)
var reBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)

func codeWithoutComments(b []byte) []byte {
	s := reBlockComment.ReplaceAll(b, nil)
	return reLineComment.ReplaceAll(s, nil)
}

func findAppVersionInFile(b []byte) (string, bool) {
	code := codeWithoutComments(b)
	if m := reAppVersionAssignment.FindSubmatch(code); len(m) == 2 {
		return string(m[1]), true
	}
	return "", false
}

type match struct {
	file    string
	version string
}

// ExtractAppVersion scans .go files under root for appversion = "..." assignments.
func ExtractAppVersion(root string) (Result, error) {
	var matches []match
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
		if v, ok := findAppVersionInFile(b); ok {
			matches = append(matches, match{
				file:    path,
				version: v,
			})
		}
		return nil
	}
	if err := filepath.WalkDir(root, walk); err != nil {
		return Result{}, fmt.Errorf("scan project for appversion: %w", err)
	}

	if len(matches) == 0 {
		return Result{}, notFoundError{root: root}
	}

	// Prefer main.go in the module root when multiple files use the same value.
	chosen := matches[0]
	for _, m := range matches[1:] {
		if m.version != chosen.version {
			return Result{}, conflictError{root: root, matches: matches}
		}
		if filepath.Base(m.file) == "main.go" && filepath.Dir(m.file) == root {
			chosen = m
		}
	}
	if chosen.version == "" {
		return Result{}, emptyError{file: chosen.file}
	}

	return Result{Version: chosen.version, File: chosen.file}, nil
}

type notFoundError struct{ root string }

func (e notFoundError) Error() string {
	return formatVersionFailure(
		fmt.Sprintf("appversion = \"...\" not found in Go sources under %s", e.root),
		"add an assignment such as appversion = \"1.2.3\" in any .go file",
		"or pass --set-version to override",
	)
}

type emptyError struct{ file string }

func (e emptyError) Error() string {
	return formatVersionFailure(
		fmt.Sprintf("appversion is empty in %s", e.file),
		"set a non-empty version string, e.g. appversion = \"1.2.3\"",
		"or pass --set-version to override",
	)
}

type conflictError struct {
	root    string
	matches []match
}

func (e conflictError) Error() string {
	checks := []string{
		fmt.Sprintf("conflicting appversion values under %s", e.root),
	}
	seen := make(map[string]bool)
	for _, m := range e.matches {
		line := fmt.Sprintf("%s → %q", m.file, m.version)
		if !seen[line] {
			checks = append(checks, line)
			seen[line] = true
		}
	}
	checks = append(checks,
		"use the same appversion value in every file",
		"or pass --set-version to override",
	)
	return formatVersionFailure(checks...)
}

func formatVersionFailure(checks ...string) string {
	var b strings.Builder
	b.WriteString("cannot determine project version:\n")
	for _, c := range checks {
		b.WriteString("  ✗ ")
		b.WriteString(c)
		b.WriteByte('\n')
	}
	return strings.TrimSuffix(b.String(), "\n")
}
