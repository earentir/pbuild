package pkgdisc

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Main is one buildable main package under a module root.
type Main struct {
	BuildPath string // argument to go build (e.g. "." or "./tools/myapp")
	Name      string // binary base name (output file prefix)
	Dir       string // absolute path to the main package directory
	MainGo    string // absolute path to main.go when present (for messages)
}

var (
	reLineComment  = regexp.MustCompile(`//.*`)
	reBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

func codeWithoutComments(b []byte) []byte {
	s := reBlockComment.ReplaceAll(b, nil)
	return reLineComment.ReplaceAll(s, nil)
}

// Discover finds every Go main package under workDir by scanning source (any layout).
func Discover(workDir, defaultName string) ([]Main, error) {
	workDir = filepath.Clean(workDir)

	mains, err := discoverByWalk(workDir)
	if err != nil {
		return nil, err
	}
	if len(mains) == 0 {
		return nil, noMainError{root: workDir}
	}

	if len(mains) == 1 && defaultName != "" {
		mains[0].Name = defaultName
	}
	sort.Slice(mains, func(i, j int) bool {
		if mains[i].BuildPath == mains[j].BuildPath {
			return mains[i].Name < mains[j].Name
		}
		return mains[i].BuildPath < mains[j].BuildPath
	})
	return mains, nil
}

func discoverByWalk(workDir string) ([]Main, error) {
	seen := make(map[string]bool)
	var mains []Main
	err := filepath.WalkDir(workDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		if shouldSkipDir(workDir, path) {
			return filepath.SkipDir
		}
		if !dirIsMainPackage(path) {
			return nil
		}
		if seen[path] {
			return nil
		}
		seen[path] = true
		m, ok := mainFromDir(workDir, path)
		if ok {
			mains = append(mains, m)
		}
		return nil
	})
	return mains, err
}

func mainFromDir(workDir, dir string) (Main, bool) {
	dir = filepath.Clean(dir)
	mainGo := filepath.Join(dir, "main.go")
	if _, err := os.Stat(mainGo); err != nil {
		mainGo = ""
	}
	rel, err := filepath.Rel(workDir, dir)
	if err != nil {
		return Main{}, false
	}
	buildPath := "."
	if rel != "." {
		buildPath = "./" + filepath.ToSlash(rel)
	}
	name := filepath.Base(dir)
	if name == "." || name == string(filepath.Separator) {
		name = filepath.Base(workDir)
	}
	return Main{
		BuildPath: buildPath,
		Name:      name,
		Dir:       dir,
		MainGo:    mainGo,
	}, true
}

func shouldSkipDir(workDir, dir string) bool {
	dir = filepath.Clean(dir)
	rel, err := filepath.Rel(workDir, dir)
	if err != nil {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return true
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, p := range parts {
		switch p {
		case "vendor", ".git", ".trunk", "node_modules":
			return true
		}
		if strings.HasPrefix(p, ".") && p != "." {
			return true
		}
	}
	return false
}

func dirIsMainPackage(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	found := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		decl := packageDecl(codeWithoutComments(b))
		if decl == "" {
			continue
		}
		if decl != "main" {
			return false
		}
		found = true
	}
	return found
}

var rePackageDecl = regexp.MustCompile(`(?m)^\s*package\s+(\w+)`)

func packageDecl(code []byte) string {
	m := rePackageDecl.FindSubmatch(code)
	if len(m) != 2 {
		return ""
	}
	return string(m[1])
}

type noMainError struct{ root string }

func (e noMainError) Error() string {
	return fmt.Sprintf(`cannot find a main package to build:
  ✗ no package main found under %s
  · add a main package anywhere in the tree (root, cmd/<name>/, tools/<name>/, etc.)`, e.root)
}

// ImportPath is an alias for BuildPath for callers that stored the field as ImportPath.
func (m Main) ImportPath() string { return m.BuildPath }
