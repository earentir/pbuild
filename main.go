package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"pbuild/appver"
	"pbuild/fsutil"
	"pbuild/gitmeta"
	"pbuild/gobuild"
	"pbuild/provenance"
	"pbuild/sign"
	"pbuild/targets"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"
	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
	"github.com/olekukonko/tablewriter/tw"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var appVersion = "1.2.25"

// getBuildMode returns the appropriate build mode for the target platform
func getBuildMode(requestedMode string) string {
	if requestedMode != "auto" {
		return requestedMode
	}

	// For auto mode, use regular executable to avoid CGO conflicts
	// PIE requires CGO, but our default strategy is purego (no CGO)
	return "exe"
}

// getBuildStrategy returns the appropriate strategy based on build mode
func getBuildStrategy(requestedStrategy, buildMode string) gobuild.BuildTagStrategy {
	// If PIE is requested, we need CGO, so force flexible strategy
	if buildMode == "pie" && requestedStrategy == "purego" {
		return gobuild.FlexibleCGO
	}
	return gobuild.ParseStrategy(requestedStrategy)
}

// compressFile compresses a file using the specified method.
// When deterministic is true and method is gzip, the gzip header ModTime is set to zero for reproducible checksums.
func compressFile(inputPath, outputPath, method string, deterministic bool) error {
	inputFile, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	defer inputFile.Close()

	outputFile, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer outputFile.Close()

	var writer io.Writer
	switch method {
	case "gzip":
		gz := gzip.NewWriter(outputFile)
		if deterministic {
			gz.Header.ModTime = time.Unix(0, 0)
		}
		writer = gz
	case "zstd":
		writer, err = zstd.NewWriter(outputFile)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported compression method: %s", method)
	}

	_, err = io.Copy(writer, inputFile)
	if err != nil {
		return err
	}

	// Close the writer to flush any remaining data
	if closer, ok := writer.(io.Closer); ok {
		err = closer.Close()
		if err != nil {
			return err
		}
	}

	return nil
}

// generateChecksums generates SHA256 and SHA512 checksums for a file
func generateChecksums(filePath string) (string, string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", "", err
	}
	defer file.Close()

	// Create hash writers
	sha256Hash := sha256.New()
	sha512Hash := sha512.New()

	// Create a multi-writer to write to both hashes simultaneously
	multiWriter := io.MultiWriter(sha256Hash, sha512Hash)

	// Copy file content to both hashers
	_, err = io.Copy(multiWriter, file)
	if err != nil {
		return "", "", err
	}

	// Get the hash sums
	sha256Sum := fmt.Sprintf("%x", sha256Hash.Sum(nil))
	sha512Sum := fmt.Sprintf("%x", sha512Hash.Sum(nil))

	return sha256Sum, sha512Sum, nil
}

// getSigningPassphrase returns the passphrase for the signing key: from PBUILD_SIGNING_PASSPHRASE env, or
// interactive prompt (no echo, like gpg). When not in a terminal, requires the env var.
func getSigningPassphrase() ([]byte, error) {
	if s := os.Getenv("PBUILD_SIGNING_PASSPHRASE"); s != "" {
		return []byte(s), nil
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, fmt.Errorf("passphrase required for signing key but stdin is not a terminal; set PBUILD_SIGNING_PASSPHRASE")
	}
	fmt.Print("Passphrase for signing key: ")
	passphrase, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return nil, fmt.Errorf("read passphrase: %w", err)
	}
	return passphrase, nil
}

// isVendorRelatedError returns true if the build error suggests out-of-date or missing vendored packages.
func isVendorRelatedError(errMsg string) bool {
	s := strings.ToLower(errMsg)
	return strings.Contains(s, "not in goroot or vendor") ||
		strings.Contains(s, "missing module") ||
		strings.Contains(s, "no required module") ||
		strings.Contains(s, "go.sum") ||
		strings.Contains(s, "checksum mismatch") ||
		strings.Contains(s, "cannot find package")
}

// ensureVendor runs go mod vendor in workDir if vendor/ is missing or empty. Returns nil if not using modules.
func ensureVendor(ctx context.Context, workDir string) error {
	if _, err := os.Stat(filepath.Join(workDir, "go.mod")); os.IsNotExist(err) {
		return nil
	}
	vendorDir := filepath.Join(workDir, "vendor")
	if fi, err := os.Stat(vendorDir); err == nil && fi.IsDir() {
		// Check if vendor is non-empty (has modules.txt)
		if _, err := os.Stat(filepath.Join(vendorDir, "modules.txt")); err == nil {
			return nil
		}
	}
	cmd := exec.CommandContext(ctx, "go", "mod", "vendor")
	cmd.Dir = workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// checkAndUpdateGitignore checks if builds/ directory is in .gitignore and adds it if missing
func checkAndUpdateGitignore(workDir string) error {
	gitignorePath := filepath.Join(workDir, ".gitignore")

	// Check if .gitignore file exists
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		// .gitignore doesn't exist, inform user and do nothing
		fmt.Println("No .gitignore file found - skipping builds/ directory check")
		return nil
	}

	// Read existing .gitignore file
	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		return fmt.Errorf("failed to read .gitignore file: %v", err)
	}

	// Check if builds/ is already in the file
	lines := strings.Split(string(content), "\n")
	buildsEntryFound := false
	for _, line := range lines {
		// Trim whitespace and check for exact match
		trimmed := strings.TrimSpace(line)
		if trimmed == "builds/" {
			buildsEntryFound = true
			break
		}
	}

	// If builds/ is not found, add it
	if !buildsEntryFound {
		// Add builds/ to the end of the file
		newContent := string(content)
		if !strings.HasSuffix(newContent, "\n") && len(newContent) > 0 {
			newContent += "\n"
		}
		newContent += "builds/\n"

		if err := os.WriteFile(gitignorePath, []byte(newContent), 0644); err != nil {
			return fmt.Errorf("failed to update .gitignore file: %v", err)
		}
		fmt.Println("Added builds/ to .gitignore file")
	} else {
		fmt.Println("builds/ directory already in .gitignore file")
	}

	return nil
}

// ArtifactEntry describes a single build artifact with optional checksums.
type ArtifactEntry struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256,omitempty"`
	SHA512 string `json:"sha512,omitempty"`
}

// BuildMetadata holds build information
type BuildMetadata struct {
	ProjectName   string                 `json:"project_name"`
	Version       string                 `json:"version"`
	BuildTime     time.Time              `json:"build_time"`
	BuildDuration string                 `json:"build_duration"`
	GoVersion     string                 `json:"go_version"`
	BuildHost     string                 `json:"build_host"`
	BuildUser     string                 `json:"build_user"`
	BuildOS       string                 `json:"build_os"`
	BuildArch     string                 `json:"build_arch"`
	Targets       []targets.Target       `json:"targets"`
	BuildConfig   gobuild.BuildConfig    `json:"build_config"`
	Flags         map[string]interface{} `json:"flags"`
	Artifacts     []ArtifactEntry        `json:"artifacts"`
	SuccessCount  int                    `json:"success_count"`
	FailCount     int                    `json:"fail_count"`
}

// ProfileConfig holds the build profile (e.g. which targets are enabled). Stored in builds/ as pbuild-profile.json.
type ProfileConfig struct {
	Version int             `json:"version"`
	Targets []ProfileTarget `json:"targets"`
}

type ProfileTarget struct {
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	Enabled bool   `json:"enabled"`
}

const profileFilename = "pbuild-profile.json"
const profileDirName = "builds" // profile always lives in the builds folder (not in version dir, not in custom output-dir)

// getProfileTargets loads the profile from the builds folder (workDir/builds/). If the file does not exist, it is created
// with all default targets enabled and justCreated is true. Returns the list of enabled targets and any error.
func getProfileTargets(workDir string) (matrix []targets.Target, justCreated bool, err error) {
	profileDir := filepath.Join(workDir, profileDirName)
	path := filepath.Join(profileDir, profileFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, false, fmt.Errorf("read profile: %w", err)
		}
		// First time: create profile with all targets enabled
		def := targets.Default()
		cfg := ProfileConfig{Version: 1, Targets: make([]ProfileTarget, len(def))}
		for i, t := range def {
			cfg.Targets[i] = ProfileTarget{OS: t.OS, Arch: t.Arch, Enabled: true}
		}
		body, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.MkdirAll(profileDir, 0o755); err != nil {
			return nil, false, fmt.Errorf("create builds dir for profile: %w", err)
		}
		if err := os.WriteFile(path, body, 0644); err != nil {
			return nil, false, fmt.Errorf("write profile: %w", err)
		}
		return def, true, nil
	}
	var cfg ProfileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, false, fmt.Errorf("parse profile: %w", err)
	}
	for _, t := range cfg.Targets {
		if t.Enabled {
			matrix = append(matrix, targets.Target{OS: t.OS, Arch: t.Arch})
		}
	}
	return matrix, false, nil
}

// writeBuildMetadata writes build metadata to a JSON file
func writeBuildMetadata(versionDir string, metadata BuildMetadata) error {
	metadataPath := filepath.Join(versionDir, "build-metadata.json")
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(metadataPath, data, 0644)
}

var (
	flagAll          bool
	flagName         string
	flagOutDir       string
	flagSetVersion   string
	flagStrategy     string
	flagAMD64Level   string
	flagARM64Level   string
	flagARMLevel     string
	flagMIPSLevel    string
	flagPPC64Level   string
	flagRISCVLevel   string
	flagBuildMode    string
	flagTags         string
	flagLDFlags      string
	flagBuildFlags   string
	flagVerbose      bool
	flagSkipCleanup  bool
	flagStopOnError  bool
	flagParallel     int
	flagCleanCache   bool
	flagCompress     string
	flagChecksums    bool
	flagReproducible   bool
	flagVendor         bool
	flagSign           bool
	flagSigningKeyFile string
	flagSigningKey     string
	flagSignArmor      bool
	flagProfile        bool
	flagProvenance     bool
)

func main() {
	root := &cobra.Command{
		Use:          "pbuild [TARGET_DIR]",
		Short:        "Cross-compile a Go project for a target matrix",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true, // do not print usage on build errors
		RunE: func(cmd *cobra.Command, args []string) error {
			target := "."
			if len(args) == 1 {
				target = args[0]
			}
			return run(target, false)
		},
	}
	// Expose tool version via built-in --version
	root.Version = appVersion
	root.SetVersionTemplate("{{.Version}}\n")
	root.Flags().BoolVar(&flagAll, "all", false, "build for all predefined targets")
	root.Flags().StringVar(&flagName, "name", "", "override inferred project name")
	root.Flags().StringVar(&flagOutDir, "output-dir", "builds", "directory for build artifacts")
	root.Flags().StringVar(&flagSetVersion, "set-version", "", "override embedded version tag")

	// Build configuration flags
	root.Flags().StringVar(&flagStrategy, "strategy", "purego", "build strategy: flexible, purego, traditional")
	root.Flags().StringVar(&flagAMD64Level, "amd64-level", "v2", "GOAMD64 level: v1, v2, v3, v4")
	root.Flags().StringVar(&flagARM64Level, "arm64-level", "v8.0", "GOARM64 level: v8.0, v8.1, v8.2, v8.3, v8.4, v8.5, v8.6, v8.7, v8.8, v8.9, v9.0, v9.1, v9.2, v9.3, v9.4, v9.5")
	root.Flags().StringVar(&flagARMLevel, "arm-level", "7", "GOARM level: 5, 6, 7")
	root.Flags().StringVar(&flagMIPSLevel, "mips-level", "hardfloat", "GOMIPS level: hardfloat, softfloat")
	root.Flags().StringVar(&flagPPC64Level, "ppc64-level", "power8", "GOPPC64 level: power8, power9, power10")
	root.Flags().StringVar(&flagRISCVLevel, "riscv-level", "rva20u64", "GORISCV64 level: rva20u64, rva22u64")
	root.Flags().StringVar(&flagBuildMode, "buildmode", "auto", "build mode: auto (exe), pie (requires CGO), exe, c-archive, c-shared")
	root.Flags().StringVar(&flagTags, "tags", "", "additional build tags (comma-separated)")
	root.Flags().StringVar(&flagLDFlags, "ldflags", "", "custom ldflags (default: -s -w -X main.appVersion)")
	root.Flags().StringVar(&flagBuildFlags, "build-flags", "", "additional go build flags (default: -trimpath)")

	// Behavior flags
	root.Flags().BoolVar(&flagVerbose, "verbose", false, "show actual go build commands")
	root.Flags().BoolVar(&flagSkipCleanup, "skip-cleanup", false, "skip cleaning previous build directory")
	root.Flags().BoolVar(&flagStopOnError, "stop-on-error", false, "stop building others when one fails")
	root.Flags().IntVar(&flagParallel, "parallel", runtime.NumCPU(), "number of parallel builds (0 = sequential)")
	root.Flags().BoolVar(&flagCleanCache, "clean-cache", false, "clean Go build cache before building")

	// Output flags
	root.Flags().StringVar(&flagCompress, "compress", "", "compress binaries: zstd, gzip")
	root.Flags().BoolVar(&flagChecksums, "checksums", true, "generate SHA256 and SHA512 checksums")

	// Reproducible and vendor
	root.Flags().BoolVar(&flagReproducible, "reproducible", false, "ensure reproducible builds (same code → same binary hash); forces -trimpath and deterministic gzip")
	root.Flags().BoolVar(&flagVendor, "vendor", false, "use vendored dependencies (-mod=vendor); create vendor/ if missing; prompt to re-vendor on failure if out of date")

	// GPG signing (pure Go OpenPGP)
	root.Flags().BoolVar(&flagSign, "sign", false, "create GPG detached signatures (.sig or .asc) for each artifact and verify after signing")
	root.Flags().StringVar(&flagSigningKeyFile, "signing-key-file", "", "path to armored private key file for signing (required when --sign)")
	root.Flags().StringVar(&flagSigningKey, "signing-key", "", "key ID (hex) to use when key file contains multiple keys")
	root.Flags().BoolVar(&flagSignArmor, "sign-armor", false, "output ASCII-armored signatures (.asc) instead of binary (.sig)")

	// Profile: use saved target list from builds folder (create on first run, then build only enabled)
	root.Flags().BoolVar(&flagProfile, "profile", false, "use profile: build targets from "+profileDirName+"/"+profileFilename+" (create with all enabled on first run; edit to disable targets)")

	// SLSA provenance (in-toto attestations)
	root.Flags().BoolVar(&flagProvenance, "provenance", false, "write SLSA provenance (provenance.intoto.jsonl) for OpenSSF/supply-chain verification")

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// showConfigTables displays the configuration in 3 side-by-side tables
func showConfigTables() {
	// Build Config table
	buildTbl := tablewriter.NewTable(
		os.Stdout,
		tablewriter.WithRenderer(renderer.NewBlueprint(tw.Rendition{
			Borders:  tw.BorderNone,
			Settings: tw.Settings{Separators: tw.Separators{BetweenColumns: tw.On, BetweenRows: tw.On}},
		})),
	)
	buildTbl.Header([]string{"Build Config", "Value"})
	buildData := [][]any{
		{"Strategy", flagStrategy},
		{"Build Mode", flagBuildMode},
	}

	// Add custom build flags if present
	if flagTags != "" {
		buildData = append(buildData, []any{"Custom Tags", flagTags})
	}
	if flagLDFlags != "" {
		buildData = append(buildData, []any{"Custom LDFlags", flagLDFlags})
	}
	if flagBuildFlags != "" {
		buildData = append(buildData, []any{"Custom Build Flags", flagBuildFlags})
	}
	if flagCompress != "" {
		buildData = append(buildData, []any{"Compression", flagCompress})
	}

	_ = buildTbl.Bulk(buildData)

	// CPU Levels table
	cpuTbl := tablewriter.NewTable(
		os.Stdout,
		tablewriter.WithRenderer(renderer.NewBlueprint(tw.Rendition{
			Borders:  tw.BorderNone,
			Settings: tw.Settings{Separators: tw.Separators{BetweenColumns: tw.On, BetweenRows: tw.On}},
		})),
	)
	cpuTbl.Header([]string{"CPU Levels", "Value"})
	cpuData := [][]any{
		{"AMD64", flagAMD64Level},
		{"ARM64", flagARM64Level},
		{"ARM", flagARMLevel},
		{"MIPS", flagMIPSLevel},
		{"PPC64", flagPPC64Level},
		{"RISC-V", flagRISCVLevel},
	}
	_ = cpuTbl.Bulk(cpuData)

	// Behavior table
	behaviorTbl := tablewriter.NewTable(
		os.Stdout,
		tablewriter.WithRenderer(renderer.NewBlueprint(tw.Rendition{
			Borders:  tw.BorderNone,
			Settings: tw.Settings{Separators: tw.Separators{BetweenColumns: tw.On, BetweenRows: tw.On}},
		})),
	)
	behaviorTbl.Header([]string{"Behavior", "Value"})
	behaviorData := [][]any{
		{"Parallel Workers", fmt.Sprintf("%d", flagParallel)},
		{"Clean Cache", fmt.Sprintf("%t", flagCleanCache)},
		{"Skip Cleanup", fmt.Sprintf("%t", flagSkipCleanup)},
		{"Stop on Error", fmt.Sprintf("%t", flagStopOnError)},
		{"Verbose", fmt.Sprintf("%t", flagVerbose)},
		{"Generate Checksums", fmt.Sprintf("%t", flagChecksums)},
		{"Reproducible", fmt.Sprintf("%t", flagReproducible)},
		{"Vendor", fmt.Sprintf("%t", flagVendor)},
		{"Sign", fmt.Sprintf("%t", flagSign)},
		{"Provenance", fmt.Sprintf("%t", flagProvenance)},
		{"Profile", fmt.Sprintf("%t", flagProfile)},
	}
	_ = behaviorTbl.Bulk(behaviorData)

	// Render tables side by side using tablewriter
	renderTablesSideBySide(buildTbl, cpuTbl, behaviorTbl)
}

// renderTablesSideBySide renders tablewriter tables side by side
func renderTablesSideBySide(buildTbl, cpuTbl, behaviorTbl *tablewriter.Table) {
	// Capture output from each table by creating new tables with buffers
	var outputs []string

	// Build Config table
	var buildBuf strings.Builder
	buildCapture := tablewriter.NewTable(
		&buildBuf,
		tablewriter.WithRenderer(renderer.NewBlueprint(tw.Rendition{
			Borders:  tw.BorderNone,
			Settings: tw.Settings{Separators: tw.Separators{BetweenColumns: tw.On, BetweenRows: tw.On}},
		})),
	)
	buildCapture.Header([]string{"Build Config", "Value"})
	buildData := [][]any{
		{"Strategy", flagStrategy},
		{"Build Mode", flagBuildMode},
	}
	if flagTags != "" {
		buildData = append(buildData, []any{"Custom Tags", flagTags})
	}
	if flagLDFlags != "" {
		buildData = append(buildData, []any{"Custom LDFlags", flagLDFlags})
	}
	if flagBuildFlags != "" {
		buildData = append(buildData, []any{"Custom Build Flags", flagBuildFlags})
	}
	if flagCompress != "" {
		buildData = append(buildData, []any{"Compression", flagCompress})
	}
	_ = buildCapture.Bulk(buildData)
	buildCapture.Render()
	outputs = append(outputs, buildBuf.String())

	// CPU Levels table
	var cpuBuf strings.Builder
	cpuCapture := tablewriter.NewTable(
		&cpuBuf,
		tablewriter.WithRenderer(renderer.NewBlueprint(tw.Rendition{
			Borders:  tw.BorderNone,
			Settings: tw.Settings{Separators: tw.Separators{BetweenColumns: tw.On, BetweenRows: tw.On}},
		})),
	)
	cpuCapture.Header([]string{"CPU Levels", "Value"})
	cpuData := [][]any{
		{"AMD64", flagAMD64Level},
		{"ARM64", flagARM64Level},
		{"ARM", flagARMLevel},
		{"MIPS", flagMIPSLevel},
		{"PPC64", flagPPC64Level},
		{"RISC-V", flagRISCVLevel},
	}
	_ = cpuCapture.Bulk(cpuData)
	cpuCapture.Render()
	outputs = append(outputs, cpuBuf.String())

	// Behavior table
	var behaviorBuf strings.Builder
	behaviorCapture := tablewriter.NewTable(
		&behaviorBuf,
		tablewriter.WithRenderer(renderer.NewBlueprint(tw.Rendition{
			Borders:  tw.BorderNone,
			Settings: tw.Settings{Separators: tw.Separators{BetweenColumns: tw.On, BetweenRows: tw.On}},
		})),
	)
	behaviorCapture.Header([]string{"Behavior", "Value"})
	behaviorData := [][]any{
		{"Parallel Workers", fmt.Sprintf("%d", flagParallel)},
		{"Clean Cache", fmt.Sprintf("%t", flagCleanCache)},
		{"Skip Cleanup", fmt.Sprintf("%t", flagSkipCleanup)},
		{"Stop on Error", fmt.Sprintf("%t", flagStopOnError)},
		{"Verbose", fmt.Sprintf("%t", flagVerbose)},
		{"Generate Checksums", fmt.Sprintf("%t", flagChecksums)},
		{"Reproducible", fmt.Sprintf("%t", flagReproducible)},
		{"Vendor", fmt.Sprintf("%t", flagVendor)},
		{"Sign", fmt.Sprintf("%t", flagSign)},
		{"Provenance", fmt.Sprintf("%t", flagProvenance)},
		{"Profile", fmt.Sprintf("%t", flagProfile)},
	}
	_ = behaviorCapture.Bulk(behaviorData)
	behaviorCapture.Render()
	outputs = append(outputs, behaviorBuf.String())

	// Split each output into lines and print them side by side
	lines := make([][]string, len(outputs))
	maxLines := 0
	tableWidths := make([]int, len(outputs))

	for i, output := range outputs {
		lines[i] = strings.Split(strings.TrimRight(output, "\n"), "\n")
		if len(lines[i]) > maxLines {
			maxLines = len(lines[i])
		}
		// Use the visual width (rune count) of the separator line (line 1) for consistent padding
		if len(lines[i]) > 1 {
			tableWidths[i] = utf8.RuneCountInString(lines[i][1])
		} else if len(lines[i]) > 0 {
			tableWidths[i] = utf8.RuneCountInString(lines[i][0])
		}
	}

	// Print lines side by side
	for lineNum := 0; lineNum < maxLines; lineNum++ {
		for i, tableLines := range lines {
			if lineNum < len(tableLines) {
				fmt.Print(tableLines[lineNum])
			} else {
				// Print empty line with same visual width as this table's separator line
				fmt.Print(strings.Repeat(" ", tableWidths[i]))
			}
			if i < len(lines)-1 {
				fmt.Print("  ") // Spacing between tables
			}
		}
		fmt.Println()
	}
}

func run(targetDir string, isRetry bool) error {
	startTime := time.Now()

	abs, err := filepath.Abs(targetDir)
	if err != nil {
		return err
	}

	// roots
	workDir := abs
	if modRoot, err := fsutil.FindModuleRoot(abs); err == nil {
		workDir = modRoot
	}
	gitRoot := workDir
	if gr, err := fsutil.FindGitRoot(workDir); err == nil {
		gitRoot = gr
	}

	// When using vendor, ensure vendor/ exists before building (create if never run before)
	if flagVendor && !isRetry {
		if err := ensureVendor(context.Background(), workDir); err != nil {
			return fmt.Errorf("go mod vendor failed: %w", err)
		}
	}

	// Signing requires a key file
	if flagSign && flagSigningKeyFile == "" {
		return fmt.Errorf("--sign requires --signing-key-file")
	}

	// name
	projectName := flagName
	if projectName == "" {
		if m, err := fsutil.InferModulePath(workDir); err == nil && m != "" {
			parts := strings.Split(m, "/")
			projectName = parts[len(parts)-1]
		} else {
			projectName = filepath.Base(workDir)
		}
	}

	// version
	versionTag := flagSetVersion
	if versionTag == "" {
		base, _ := appver.ExtractAppVersion(workDir)
		if base == "" {
			base = appVersion
		}
		rev, _ := gitmeta.ResolveHEAD(gitRoot)
		if rev == "" {
			rev = "unknown"
		}
		dirty, _ := gitmeta.HeuristicDirty(gitRoot)
		if dirty {
			rev += "-dirty"
		}
		versionTag = fmt.Sprintf("%s-%s", base, rev)
	}

	// Check and update .gitignore to ensure builds/ directory is ignored
	if err := checkAndUpdateGitignore(workDir); err != nil {
		fmt.Printf("Warning: Failed to check/update .gitignore: %v\n", err)
	}

	// out dir for build artifacts (version dirs go here)
	outDir := flagOutDir
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(workDir, outDir)
	}

	// matrix: from profile (builds/pbuild-profile.json), or --all, or current platform only. Do this before creating versionDir.
	var matrix []targets.Target
	if flagProfile {
		var justCreated bool
		matrix, justCreated, err = getProfileTargets(workDir)
		if err != nil {
			return err
		}
		profilePath := filepath.Join(workDir, profileDirName, profileFilename)
		if justCreated {
			fmt.Printf("Profile created: %s\n", profilePath)
			fmt.Println("Edit the file and set \"enabled\": false for targets you don't want, then run with --profile again.")
			return nil
		}
		if len(matrix) == 0 {
			return fmt.Errorf("profile has no enabled targets; edit %s and set \"enabled\": true for at least one target", profilePath)
		}
	} else if flagAll {
		matrix = targets.Default()
	} else {
		matrix = []targets.Target{{OS: runtime.GOOS, Arch: runtime.GOARCH}}
	}

	versionDir := filepath.Join(outDir, versionTag)
	if !flagSkipCleanup {
		_ = os.RemoveAll(versionDir)
	}
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		return err
	}

	fmt.Printf("Building version %s\n\n", versionTag)

	// Show build configuration in 3 side-by-side tables
	showConfigTables()
	fmt.Println()

	// collect rows for summary table (errMsg used to detect vendor-related failures)
	type row struct{ file, target, size, sha256, sha512, status, errMsg string }
	var rows []row

	// status glyphs
	greenTick := "\x1b[32m✓\x1b[0m"
	redX := "\x1b[31m✗\x1b[0m"

	var successCount, failCount int

	ctx := context.Background()

	// Determine number of workers
	numWorkers := flagParallel
	if numWorkers <= 0 {
		numWorkers = 1 // Sequential
	}

	// Channel for targets
	targetChan := make(chan targets.Target, len(matrix))
	resultChan := make(chan row, len(matrix))

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for t := range targetChan {
				outName := targets.OutputName(projectName, t)
				outPath := filepath.Join(versionDir, outName)

				if flagVerbose {
					fmt.Printf("[Worker %d] Building for: %s/%s -> %s\n", workerID, t.OS, t.Arch, outPath)
				} else {
					fmt.Printf("Building for: %s/%s -> %s\n", t.OS, t.Arch, outPath)
				}

				// Build configuration
				buildMode := getBuildMode(flagBuildMode)
				strategy := getBuildStrategy(flagStrategy, buildMode)

				// Warn if strategy was changed due to PIE requirements
				if buildMode == "pie" && flagStrategy == "purego" {
					if flagVerbose {
						fmt.Printf("[Worker %d]   WARNING: PIE mode requires CGO, switching from purego to flexible strategy\n", workerID)
					}
				}

				config := gobuild.BuildConfig{
					Strategy:     strategy,
					AMD64Level:   flagAMD64Level,
					ARM64Level:   flagARM64Level,
					ARMLevel:     flagARMLevel,
					MIPSLevel:    flagMIPSLevel,
					PPC64Level:   flagPPC64Level,
					RISCVLevel:   flagRISCVLevel,
					BuildMode:    buildMode,
					Tags:         flagTags,
					LDFlags:      flagLDFlags,
					BuildFlags:   flagBuildFlags,
					Verbose:      flagVerbose,
					CleanCache:   flagCleanCache,
					Reproducible: flagReproducible,
					UseVendor:    flagVendor,
				}

				// Set default ldflags if not provided
				if config.LDFlags == "" {
					config.LDFlags = "-s -w -X main.appVersion=" + versionTag
				}

				if err := gobuild.BuildWithConfig(ctx, workDir, t, outPath, config); err != nil {
					errStr := err.Error()
					if flagVerbose {
						fmt.Printf("[Worker %d]   FAILED\n  %v\n\n", workerID, err)
					} else {
						fmt.Printf("  FAILED\n  %v\n\n", err)
					}
					resultChan <- row{
						file:   outName,
						target: t.OS + "/" + t.Arch,
						size:   "n/a",
						sha256: "n/a",
						sha512: "",
						status: redX,
						errMsg: errStr,
					}
					continue
				}

				_ = os.Chmod(outPath, 0o755)

				// Compress if requested
				if flagCompress != "" {
					ext := ""
					switch flagCompress {
					case "gzip":
						ext = ".gz"
					case "zstd":
						ext = ".zst"
					}
					compressedPath := outPath + ext
					if err := compressFile(outPath, compressedPath, flagCompress, flagReproducible); err != nil {
						if flagVerbose {
							fmt.Printf("[Worker %d]   Compression failed: %v\n", workerID, err)
						}
					} else {
						// Remove original file after successful compression
						os.Remove(outPath)
						outPath = compressedPath
						if flagVerbose {
							fmt.Printf("[Worker %d]   Compressed to %s\n", workerID, compressedPath)
						}
					}
				}

				if flagVerbose {
					fmt.Printf("[Worker %d]   SUCCESS\n\n", workerID)
				} else {
					fmt.Printf("  SUCCESS\n\n")
				}

				sizeStr := "n/a"
				sha256Str := "n/a"
				sha512Str := ""
				if sz, err := fsutil.FileSize(outPath); err == nil {
					sizeStr = fmt.Sprintf("%s (%d)", fsutil.HumanSizeBytes(sz), sz)
				}

				// Generate checksums if requested (stored in build-metadata.json, not .hash files)
				if flagChecksums {
					sha256Sum, sha512Sum, err := generateChecksums(outPath)
					if err != nil {
						if flagVerbose {
							fmt.Printf("[Worker %d]   Checksum generation failed: %v\n", workerID, err)
						}
					} else {
						sha256Str = sha256Sum
						sha512Str = sha512Sum
					}
				}

				// Update outName if compressed
				finalOutName := outName
				if flagCompress != "" {
					ext := ""
					switch flagCompress {
					case "gzip":
						ext = ".gz"
					case "zstd":
						ext = ".zst"
					}
					finalOutName = outName + ext
				}

				resultChan <- row{
					file:   finalOutName,
					target: t.OS + "/" + t.Arch,
					size:   sizeStr,
					sha256: sha256Str,
					sha512: sha512Str,
					status: greenTick,
				}
			}
		}(i)
	}

	// Send targets to workers
	go func() {
		defer close(targetChan)
		for _, t := range matrix {
			targetChan <- t
		}
	}()

	// Close result channel when all workers are done
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results
	for result := range resultChan {
		rows = append(rows, result)
		if result.status == redX {
			failCount++
		} else {
			successCount++
		}
	}

	// If builds failed with --vendor and errors look like stale vendor, offer to re-vendor and retry once
	if failCount > 0 && flagVendor && !isRetry {
		var anyVendorRelated bool
		for _, r := range rows {
			if r.status == redX && isVendorRelatedError(r.errMsg) {
				anyVendorRelated = true
				break
			}
		}
		if anyVendorRelated {
			fmt.Print("\nBuild failed; may be due to out-of-date vendored packages. Re-vendor and retry? (y/n): ")
			scanner := bufio.NewScanner(os.Stdin)
			if scanner.Scan() {
				line := strings.TrimSpace(strings.ToLower(scanner.Text()))
				if line == "y" || line == "yes" {
					cmd := exec.CommandContext(ctx, "go", "mod", "vendor")
					cmd.Dir = workDir
					cmd.Stdout = os.Stdout
					cmd.Stderr = os.Stderr
					if err := cmd.Run(); err != nil {
						fmt.Fprintf(os.Stderr, "go mod vendor failed: %v\n", err)
					} else {
						fmt.Println("Re-running build after re-vendor...")
						return run(targetDir, true)
					}
				}
			}
		}
	}

	fmt.Printf("\nArtifacts for %s, version %s\nstored in %s\n\n", projectName, versionTag, versionDir)

	// render table — inner grid only, no outer frame
	tbl := tablewriter.NewTable(
		os.Stdout,
		tablewriter.WithRenderer(renderer.NewBlueprint(tw.Rendition{
			Borders:  tw.BorderNone,
			Settings: tw.Settings{Separators: tw.Separators{BetweenColumns: tw.On, BetweenRows: tw.On}},
		})),
	)

	tbl.Header([]string{"File", "Target", "Size", "SHA256", "Status"})
	data := make([][]any, 0, len(rows))
	for _, r := range rows {
		data = append(data, []any{r.file, r.target, r.size, r.sha256, r.status})
	}
	_ = tbl.Bulk(data)
	_ = tbl.Render()

	// print build summary counts
	total := successCount + failCount
	fmt.Println()
	fmt.Printf("Build summary: Total: %d  Success: %d  Failed: %d\n\n", total, successCount, failCount)

	// Generate build metadata
	buildTime := time.Now()
	hostname, _ := os.Hostname()
	username := os.Getenv("USER")
	if username == "" {
		username = os.Getenv("USERNAME") // Windows
	}

	// Collect artifact names for signing and artifact entries (with hashes) for metadata
	var artifactNames []string
	var artifactEntries []ArtifactEntry
	for _, r := range rows {
		if r.status == greenTick {
			artifactNames = append(artifactNames, r.file)
			ent := ArtifactEntry{Name: r.file}
			if r.sha256 != "n/a" && r.sha256 != "" {
				ent.SHA256 = r.sha256
			}
			if r.sha512 != "" {
				ent.SHA512 = r.sha512
			}
			artifactEntries = append(artifactEntries, ent)
		}
	}

	// SLSA provenance: single provenance.intoto.jsonl with one Statement per artifact
	var provenancePath string
	if flagProvenance && len(artifactEntries) > 0 {
		h := sha256.Sum256([]byte(versionTag + "|" + projectName + "|" + startTime.UTC().Format(time.RFC3339Nano)))
		invocationID := hex.EncodeToString(h[:])[:32]
		targetList := make([]provenance.Target, len(matrix))
		for i, t := range matrix {
			targetList[i] = provenance.Target{OS: t.OS, Arch: t.Arch}
		}
		artifacts := make([]provenance.Artifact, len(artifactEntries))
		for i, e := range artifactEntries {
			artifacts[i] = provenance.Artifact{Name: e.Name, SHA256: e.SHA256, SHA512: e.SHA512}
		}
		externalParams := map[string]interface{}{
			"version": versionTag, "project": projectName, "strategy": flagStrategy,
			"output_dir": flagOutDir, "compress": flagCompress, "checksums": flagChecksums,
			"reproducible": flagReproducible, "vendor": flagVendor, "profile": flagProfile,
		}
		internalParams := map[string]interface{}{
			"host": hostname, "user": username, "go_version": runtime.Version(),
			"pbuild_version": appVersion, "build_os": runtime.GOOS, "build_arch": runtime.GOARCH,
			"parallel": flagParallel,
		}
		path, err := provenance.Write(&provenance.Inputs{
			VersionDir:     versionDir,
			InvocationID:   invocationID,
			StartedOn:      startTime,
			FinishedOn:     buildTime,
			ProjectName:    projectName,
			VersionTag:     versionTag,
			Hostname:       hostname,
			Username:       username,
			GoVersion:      runtime.Version(),
			PbuildVersion:  appVersion,
			ExternalParams: externalParams,
			InternalParams: internalParams,
			Targets:        targetList,
			Artifacts:      artifacts,
		})
		if err != nil {
			fmt.Printf("Warning: Failed to write provenance: %v\n", err)
		} else {
			provenancePath = path
			fmt.Printf("Provenance written to: %s\n\n", path)
		}
	}

	// GPG sign artifacts (and verify each signature)
	if flagSign && len(artifactNames) > 0 {
		passphrase, err := getSigningPassphrase()
		if err != nil {
			return err
		}
		keyPath := flagSigningKeyFile
		if !filepath.IsAbs(keyPath) {
			keyPath = filepath.Join(workDir, keyPath)
		}
		entity, err := sign.LoadSigningEntity(keyPath, flagSigningKey, passphrase)
		if err != nil {
			return fmt.Errorf("load signing key: %w", err)
		}
		// Write public key into build dir so consumers can verify (e.g. GitHub release convention)
		publicKeyPath := filepath.Join(versionDir, sign.PublicKeyFilename)
		if err := sign.ExportPublicKey(entity, publicKeyPath); err != nil {
			return fmt.Errorf("export public key: %w", err)
		}
		sigExt := ".sig"
		if flagSignArmor {
			sigExt = ".asc"
		}
		if err := sign.SignArtifacts(entity, versionDir, artifactNames, sigExt); err != nil {
			return fmt.Errorf("sign artifacts: %w", err)
		}
		if provenancePath != "" {
			sigPath := sign.SignaturePath(provenancePath, sigExt)
			if err := sign.SignAndVerify(entity, provenancePath, sigPath, sigExt); err != nil {
				return fmt.Errorf("sign provenance: %w", err)
			}
			fmt.Printf("Signed provenance: %s\n", sigPath)
		}
		fmt.Printf("Signed %d artifact(s) with GPG (signatures verified). Wrote %s.\n\n", len(artifactNames), sign.PublicKeyFilename)
	}

	metadata := BuildMetadata{
		ProjectName:   projectName,
		Version:       versionTag,
		BuildTime:     buildTime,
		BuildDuration: time.Since(startTime).String(),
		GoVersion:     runtime.Version(),
		BuildHost:     hostname,
		BuildUser:     username,
		BuildOS:       runtime.GOOS,
		BuildArch:     runtime.GOARCH,
		Targets:       matrix,
		BuildConfig: gobuild.BuildConfig{
			Strategy:     gobuild.ParseStrategy(flagStrategy),
			AMD64Level:   flagAMD64Level,
			ARM64Level:   flagARM64Level,
			ARMLevel:     flagARMLevel,
			MIPSLevel:    flagMIPSLevel,
			PPC64Level:   flagPPC64Level,
			RISCVLevel:   flagRISCVLevel,
			BuildMode:    flagBuildMode, // Show the requested mode, not the resolved one
			Tags:         flagTags,
			LDFlags:      flagLDFlags,
			BuildFlags:   flagBuildFlags,
			Verbose:      flagVerbose,
			CleanCache:   flagCleanCache,
			Reproducible: flagReproducible,
			UseVendor:    flagVendor,
		},
		Flags: map[string]interface{}{
			"all":           flagAll,
			"name":          flagName,
			"output_dir":    flagOutDir,
			"set_version":   flagSetVersion,
			"tool_version":  appVersion,
			"strategy":      flagStrategy,
			"amd64_level":   flagAMD64Level,
			"arm64_level":   flagARM64Level,
			"arm_level":     flagARMLevel,
			"mips_level":    flagMIPSLevel,
			"ppc64_level":   flagPPC64Level,
			"riscv_level":   flagRISCVLevel,
			"buildmode":     flagBuildMode,
			"tags":          flagTags,
			"ldflags":       flagLDFlags,
			"build_flags":   flagBuildFlags,
			"verbose":       flagVerbose,
			"skip_cleanup":  flagSkipCleanup,
			"stop_on_error": flagStopOnError,
			"parallel":      flagParallel,
			"clean_cache":   flagCleanCache,
			"compress":      flagCompress,
			"checksums":     flagChecksums,
			"reproducible":  flagReproducible,
			"vendor":        flagVendor,
			"sign":          flagSign,
			"signing_key_file": flagSigningKeyFile,
			"signing_key":   flagSigningKey,
			"sign_armor":    flagSignArmor,
			"provenance":    flagProvenance,
			"profile":       flagProfile,
		},
		Artifacts:    artifactEntries,
		SuccessCount: successCount,
		FailCount:    failCount,
	}

	if err := writeBuildMetadata(versionDir, metadata); err != nil {
		fmt.Printf("Warning: Failed to write build metadata: %v\n", err)
	} else {
		fmt.Printf("Build metadata written to: %s/build-metadata.json\n\n", versionDir)
	}

	return nil
}
