package targets

import "fmt"

type Target struct{ OS, Arch string }

func Default() []Target {
	return []Target{
		{"linux", "amd64"},
		{"linux", "arm64"},
		{"linux", "riscv64"},
		{"windows", "amd64"},
		{"windows", "arm64"},
		{"darwin", "amd64"},
		{"darwin", "arm64"},
		{"freebsd", "amd64"},
		{"freebsd", "arm64"},
		{"freebsd", "riscv64"},
		{"openbsd", "amd64"},
		{"openbsd", "arm64"},
		{"openbsd", "riscv64"},
		{"netbsd", "amd64"},
		{"netbsd", "arm64"},
	}
}

func OutputName(project string, t Target) string {
	ext := ""
	if t.OS == "windows" {
		ext = ".exe"
	}
	if (t.OS == "windows" && t.Arch == "amd64") || (t.OS == "linux" && t.Arch == "amd64") {
		return project + ext
	}
	return fmt.Sprintf("%s-%s-%s%s", project, t.Arch, t.OS, ext)
}
