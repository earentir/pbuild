package gobuild

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"pbuild/targets"
)

// BuildTagStrategy defines how to handle CGO and static linking
type BuildTagStrategy int

const (
	// FlexibleCGO allows CGO but forces static linking for network/OS operations
	FlexibleCGO BuildTagStrategy = iota
	// NoCGOEver completely disables CGO using purego tag
	NoCGOEver
	// TraditionalCGO uses CGO_ENABLED=0 environment variable
	TraditionalCGO
)

// ParseStrategy converts a string to BuildTagStrategy
func ParseStrategy(s string) BuildTagStrategy {
	switch strings.ToLower(s) {
	case "flexible":
		return FlexibleCGO
	case "purego":
		return NoCGOEver
	case "traditional":
		return TraditionalCGO
	default:
		return NoCGOEver // Changed default to purego
	}
}

// getBuildTags returns the appropriate build tags for the strategy
func getBuildTags(strategy BuildTagStrategy) string {
	switch strategy {
	case FlexibleCGO:
		return "netgo,osusergo"
	case NoCGOEver:
		return "purego,netgo,osusergo"
	case TraditionalCGO:
		return "" // Will use CGO_ENABLED=0 instead
	default:
		return "netgo,osusergo"
	}
}

// BuildConfig holds all build configuration options
type BuildConfig struct {
	Strategy   BuildTagStrategy
	AMD64Level string
	ARM64Level string
	ARMLevel   string
	MIPSLevel  string
	PPC64Level string
	RISCVLevel string
	BuildMode  string
	Tags       string
	LDFlags    string
	BuildFlags string
	Verbose    bool
	CleanCache bool
}

func Build(ctx context.Context, workDir string, t targets.Target, outputPath, ldflags string) error {
	config := BuildConfig{
		Strategy:   NoCGOEver, // Changed default to purego
		AMD64Level: "v2",
		ARM64Level: "v8.0",
		ARMLevel:   "7",
		MIPSLevel:  "hardfloat",
		PPC64Level: "power8",
		RISCVLevel: "rva20u64",
		BuildMode:  "exe",
		LDFlags:    ldflags,
		BuildFlags: "-trimpath",
		CleanCache: true,
	}
	return BuildWithConfig(ctx, workDir, t, outputPath, config)
}

func BuildWithConfig(ctx context.Context, workDir string, t targets.Target, outputPath string, config BuildConfig) error {
	// Clean cache if requested
	if config.CleanCache {
		cleanCmd := exec.CommandContext(ctx, "go", "clean", "-cache")
		cleanCmd.Dir = workDir
		cleanCmd.Run() // Ignore errors, cache cleaning is best effort
	}

	// Build command arguments
	buildArgs := []string{"build"}

	// Add build flags
	if config.BuildFlags != "" {
		buildArgs = append(buildArgs, config.BuildFlags)
	} else {
		buildArgs = append(buildArgs, "-trimpath")
	}

	// Add build mode
	buildArgs = append(buildArgs, "-buildmode="+config.BuildMode)

	// Add build tags
	var allTags []string
	if strategyTags := getBuildTags(config.Strategy); strategyTags != "" {
		allTags = append(allTags, strategyTags)
	}
	if config.Tags != "" {
		allTags = append(allTags, config.Tags)
	}
	if len(allTags) > 0 {
		buildArgs = append(buildArgs, "-tags", strings.Join(allTags, ","))
	}

	// Add ldflags
	buildArgs = append(buildArgs, "-ldflags", config.LDFlags, "-o", outputPath, ".")

	cmd := exec.CommandContext(ctx, "go", buildArgs...)
	cmd.Dir = workDir

	env := append(os.Environ(),
		"GOOS="+t.OS,
		"GOARCH="+t.Arch,
	)

	// Handle CGO based on strategy
	if config.Strategy != FlexibleCGO {
		env = append(env, "CGO_ENABLED=0")
	}

	// Add CPU feature support based on architecture
	switch t.Arch {
	case "amd64":
		env = append(env, "GOAMD64="+config.AMD64Level)
	case "arm64":
		env = append(env, "GOARM64="+config.ARM64Level)
	case "arm":
		env = append(env, "GOARM="+config.ARMLevel)
	case "mips", "mipsle":
		env = append(env, "GOMIPS="+config.MIPSLevel)
	case "ppc64", "ppc64le":
		env = append(env, "GOPPC64="+config.PPC64Level)
	case "riscv64":
		env = append(env, "GORISCV64="+config.RISCVLevel)
	}

	// If no go.mod in workDir, force GOPATH mode so plain packages still build.
	if _, err := os.Stat(filepath.Join(workDir, "go.mod")); err != nil {
		env = append(env, "GO111MODULE=off")
	}

	cmd.Env = env

	// Show command if verbose
	if config.Verbose {
		fmt.Printf("  Command: go %s\n", strings.Join(buildArgs, " "))
		fmt.Printf("  Environment: GOOS=%s GOARCH=%s", t.OS, t.Arch)

		// Show architecture-specific environment variables
		switch t.Arch {
		case "amd64":
			fmt.Printf(" GOAMD64=%s", config.AMD64Level)
		case "arm64":
			fmt.Printf(" GOARM64=%s", config.ARM64Level)
		case "arm":
			fmt.Printf(" GOARM=%s", config.ARMLevel)
		case "mips", "mipsle":
			fmt.Printf(" GOMIPS=%s", config.MIPSLevel)
		case "ppc64", "ppc64le":
			fmt.Printf(" GOPPC64=%s", config.PPC64Level)
		case "riscv64":
			fmt.Printf(" GORISCV64=%s", config.RISCVLevel)
		}

		if config.Strategy != FlexibleCGO {
			fmt.Printf(" CGO_ENABLED=0")
		}
		fmt.Println()
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go build failed for %s/%s in %s: %v\n%s", t.OS, t.Arch, workDir, err, string(out))
	}
	return nil
}

// Legacy function for backward compatibility
func BuildWithStrategy(ctx context.Context, workDir string, t targets.Target, outputPath, ldflags string, strategy BuildTagStrategy) error {
	config := BuildConfig{
		Strategy:   strategy,
		AMD64Level: "v2",
		ARM64Level: "v8.0",
		BuildMode:  "pie",
		LDFlags:    ldflags,
		BuildFlags: "-trimpath",
		CleanCache: true,
	}
	return BuildWithConfig(ctx, workDir, t, outputPath, config)
}
