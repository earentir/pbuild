package appver

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"pbuild/pkgdisc"
)

// reAppVersionAssignment matches appversion = "..." in code (case-insensitive identifier).
var reAppVersionAssignment = regexp.MustCompile(`(?i)\bappversion\b\s*=\s*"([^"]*)"`)
var reLineComment = regexp.MustCompile(`//.*`)
var reBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)

func codeWithoutComments(b []byte) []byte {
	s := reBlockComment.ReplaceAll(b, nil)
	return reLineComment.ReplaceAll(s, nil)
}

func findAppVersionInSource(b []byte) (string, bool) {
	code := codeWithoutComments(b)
	if m := reAppVersionAssignment.FindSubmatch(code); len(m) == 2 {
		return string(m[1]), true
	}
	return "", false
}

// ExtractFromPackageDir finds appversion in any .go file in a main package directory.
func ExtractFromPackageDir(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read main package dir %s: %w", dir, err)
	}
	var found string
	var foundFile string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		v, ok := findAppVersionInSource(b)
		if !ok {
			continue
		}
		if v == "" {
			return "", emptyInFileError{file: path}
		}
		if found != "" && found != v {
			return "", fmt.Errorf("conflicting appversion in %s: %q (%s) vs %q (%s)", dir, found, foundFile, v, path)
		}
		found = v
		foundFile = path
	}
	if found == "" {
		hint := filepath.Join(dir, "main.go")
		return "", missingInFileError{file: hint}
	}
	return found, nil
}

// ExtractFromFile reads appversion from a single Go file.
func ExtractFromFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	v, ok := findAppVersionInSource(b)
	if !ok {
		return "", missingInFileError{file: path}
	}
	if v == "" {
		return "", emptyInFileError{file: path}
	}
	return v, nil
}

// Plan ties a main package to its semver base (before git suffix).
type Plan struct {
	Main pkgdisc.Main
	Base string
}

// ResolvePlans resolves appversion for each main package under workDir.
// overrideBase is the raw --set-version value; when non-empty it is used as the full version tag by the caller, not as semver base.
func ResolvePlans(workDir, defaultName, overrideBase string) ([]Plan, error) {
	mains, err := pkgdisc.Discover(workDir, defaultName)
	if err != nil {
		return nil, err
	}

	var plans []Plan
	var missing []string
	var empty []string

	for _, m := range mains {
		if overrideBase != "" {
			plans = append(plans, Plan{Main: m, Base: overrideBase})
			continue
		}
		base, err := ExtractFromPackageDir(m.Dir)
		if err != nil {
			switch err.(type) {
			case missingInFileError:
				missing = append(missing, m.Dir)
				continue
			case emptyInFileError:
				empty = append(empty, m.Dir)
				continue
			default:
				return nil, err
			}
		}
		plans = append(plans, Plan{Main: m, Base: base})
	}

	if len(missing) > 0 || len(empty) > 0 {
		return nil, resolveFailure{missing: missing, empty: empty}
	}
	if len(plans) == 0 {
		return nil, notFoundError{root: workDir}
	}
	return plans, nil
}

type missingInFileError struct{ file string }

func (e missingInFileError) Error() string {
	return fmt.Sprintf("appversion assignment not found in %s", e.file)
}

type emptyInFileError struct{ file string }

func (e emptyInFileError) Error() string {
	return fmt.Sprintf("appversion is empty in %s", e.file)
}

type notFoundError struct{ root string }

func (e notFoundError) Error() string {
	return formatVersionFailure(
		fmt.Sprintf("appversion = \"...\" not found in any main package under %s", e.root),
		"add appversion = \"1.2.3\" in each cmd/*/main.go (or root main.go)",
		"or pass --set-version to override",
	)
}

type resolveFailure struct {
	missing []string
	empty   []string
}

func (e resolveFailure) Error() string {
	checks := []string{"one or more main packages are missing a version:"}
	for _, f := range e.missing {
		checks = append(checks, fmt.Sprintf("appversion = \"...\" not found in main package %s", f))
	}
	for _, f := range e.empty {
		checks = append(checks, fmt.Sprintf("appversion is empty in main package %s", f))
	}
	checks = append(checks, "or pass --set-version to override")
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

// LDFlagsVersionInject returns -X flags for main package version variables.
func LDFlagsVersionInject(versionTag string) string {
	return "-X main.appVersion=" + versionTag + " -X main.appversion=" + versionTag + " -X main.Version=" + versionTag
}

// RelativeMainGo returns main.go path relative to workDir for error messages.
func RelativeMainGo(workDir, mainGo string) string {
	if rel, err := filepath.Rel(workDir, mainGo); err == nil {
		return rel
	}
	return mainGo
}
