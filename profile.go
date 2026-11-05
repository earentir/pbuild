package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"pbuild/fsutil"
	"pbuild/pkgdisc"
	"pbuild/targets"
)

const (
	profileFilename = "pbuild-profile.json"
	profileDirName  = "builds"
)

const profileVersion = 6

// profilePlatforms is loaded from the profile when using --profile.
var profilePlatforms []ProfilePlatform

// ProfileConfig is stored in builds/pbuild-profile.json.
type ProfileConfig struct {
	Version   int                    `json:"version"`
	OutputDir string                 `json:"output_dir"`
	Name      string                 `json:"name"`
	Targets   []ProfilePlatform      `json:"targets"`
	Build     ProfileBuildSettings   `json:"build"`
	Release   ProfileReleaseSettings `json:"release"`
}

// ProfileReleaseSettings holds GitHub/Gitea release upload defaults (same semantics as CLI).
type ProfileReleaseSettings struct {
	Forge            string `json:"forge"` // "github" or "gitea" (forgejo uses gitea)
	APIURL           string `json:"api_url"`
	UploadAfterBuild bool   `json:"upload_after_build"`
	CreateIfMissing  bool   `json:"create_if_missing"`
	Repo             string `json:"repo"`
	Tag              string `json:"tag"`
	Draft            bool   `json:"draft"`
	Notes            string `json:"notes"`
	NotesFile        string `json:"notes_file"`
}

// ProfilePlatform is one OS/arch row with nested builds for that platform.
type ProfilePlatform struct {
	OS      string                 `json:"os"`
	Arch    string                 `json:"arch"`
	Enabled bool                   `json:"enabled"`
	Builds  []ProfilePlatformBuild `json:"builds"`
}

// ProfilePlatformBuild is one main package built for a platform.
type ProfilePlatformBuild struct {
	Package string `json:"package"` // go build path (e.g. ".", "./cmd/foo")
	// Filename is the exact artifact name written under builds/<version>/ for this os/arch row.
	// Empty → pbuild uses the legacy OutputName rules from the main binary name.
	Filename string `json:"filename"`
}

// ProfileBuildSettings holds toolchain/build flags (same names as CLI).
type ProfileBuildSettings struct {
	Strategy       string `json:"strategy"`
	AMD64Level     string `json:"amd64_level"`
	ARM64Level     string `json:"arm64_level"`
	ARMLevel       string `json:"arm_level"`
	MIPSLevel      string `json:"mips_level"`
	PPC64Level     string `json:"ppc64_level"`
	RISCVLevel     string `json:"riscv_level"`
	BuildMode      string `json:"buildmode"`
	Tags           string `json:"tags"`
	LDFlags        string `json:"ldflags"`
	BuildFlags     string `json:"build_flags"`
	Compress       string `json:"compress"`
	Parallel       int    `json:"parallel"`
	Verbose        bool   `json:"verbose"`
	SkipCleanup    bool   `json:"skip_cleanup"`
	StopOnError    bool   `json:"stop_on_error"`
	CleanCache     bool   `json:"clean_cache"`
	Checksums      bool   `json:"checksums"`
	Reproducible   bool   `json:"reproducible"`
	Vendor         bool   `json:"vendor"`
	Sign           bool   `json:"sign"`
	SigningKeyFile string `json:"signing_key_file"`
	SigningKey     string `json:"signing_key"`
	SignArmor      bool   `json:"sign_armor"`
	Provenance     bool   `json:"provenance"`
}

func defaultProfileReleaseSettings() ProfileReleaseSettings {
	return ProfileReleaseSettings{
		Forge: "github",
	}
}

func defaultProfileBuildSettings() ProfileBuildSettings {
	return ProfileBuildSettings{
		Strategy:     "purego",
		AMD64Level:   "v2",
		ARM64Level:   "v8.0",
		ARMLevel:     "7",
		MIPSLevel:    "hardfloat",
		PPC64Level:   "power8",
		RISCVLevel:   "rva20u64",
		BuildMode:    "auto",
		Parallel:     runtime.NumCPU(),
		Checksums:    true,
	}
}

func inferProfileDefaultName(workDir string) string {
	if m, err := fsutil.InferModulePath(workDir); err == nil && m != "" {
		parts := strings.Split(m, "/")
		return parts[len(parts)-1]
	}
	return filepath.Base(workDir)
}

func mainPackageEntries(workDir string) []ProfilePlatformBuild {
	name := inferProfileDefaultName(workDir)
	mains, err := pkgdisc.Discover(workDir, name)
	if err != nil || len(mains) == 0 {
		return []ProfilePlatformBuild{{Package: ".", Filename: ""}}
	}
	out := make([]ProfilePlatformBuild, len(mains))
	for i, m := range mains {
		out[i] = ProfilePlatformBuild{Package: m.BuildPath, Filename: ""}
	}
	return out
}

func baseNameForPackage(workDir, pkg string) string {
	name := inferProfileDefaultName(workDir)
	mains, err := pkgdisc.Discover(workDir, name)
	if err != nil {
		return name
	}
	pkg = normalizeProfilePackage(pkg)
	for _, m := range mains {
		if normalizeProfilePackage(m.BuildPath) == pkg {
			return m.Name
		}
	}
	return name
}

func buildsWithExactFilenames(entries []ProfilePlatformBuild, t targets.Target, workDir string) []ProfilePlatformBuild {
	out := make([]ProfilePlatformBuild, len(entries))
	for i, e := range entries {
		base := e.Filename
		if base == "" {
			base = baseNameForPackage(workDir, e.Package)
		}
		out[i] = ProfilePlatformBuild{
			Package:  normalizeProfilePackage(e.Package),
			Filename: targets.OutputName(base, t),
		}
	}
	return out
}

func defaultPlatformTargets(workDir string, allEnabled bool) []ProfilePlatform {
	def := targets.Default()
	entries := mainPackageEntries(workDir)
	out := make([]ProfilePlatform, len(def))
	for i, t := range def {
		out[i] = ProfilePlatform{
			OS:      t.OS,
			Arch:    t.Arch,
			Enabled: allEnabled,
			Builds:  buildsWithExactFilenames(entries, t, workDir),
		}
	}
	return out
}

func newDefaultProfileConfig(workDir string) ProfileConfig {
	return ProfileConfig{
		Version:   profileVersion,
		OutputDir: "builds",
		Build:     defaultProfileBuildSettings(),
		Release:   defaultProfileReleaseSettings(),
		Targets:   defaultPlatformTargets(workDir, true),
	}
}

func loadProfile(workDir string) (cfg ProfileConfig, matrix []targets.Target, justCreated bool, err error) {
	path := filepath.Join(workDir, profileDirName, profileFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return ProfileConfig{}, nil, false, fmt.Errorf("read profile: %w", err)
		}
		cfg = newDefaultProfileConfig(workDir)
		fillProfileDefaults(&cfg)
		if err := writeProfileFile(path, cfg); err != nil {
			return ProfileConfig{}, nil, false, err
		}
		return cfg, enabledPlatforms(cfg.Targets), true, nil
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return ProfileConfig{}, nil, false, fmt.Errorf("parse profile: %w", err)
	}

	if profileNeedsLegacyConversion(data) {
		cfg, err = convertLegacyProfileToCurrent(data, workDir)
		if err != nil {
			return ProfileConfig{}, nil, false, fmt.Errorf("convert legacy profile: %w", err)
		}
		if err := writeProfileFile(path, cfg); err != nil {
			return ProfileConfig{}, nil, false, err
		}
		fmt.Printf("Profile converted to current format: %s\n", path)
	}

	fillProfileDefaults(&cfg)

	return cfg, enabledPlatforms(cfg.Targets), false, nil
}

func enabledPlatforms(platforms []ProfilePlatform) []targets.Target {
	var matrix []targets.Target
	for _, p := range platforms {
		if p.Enabled && len(p.Builds) > 0 {
			matrix = append(matrix, targets.Target{OS: p.OS, Arch: p.Arch})
		}
	}
	return matrix
}

func writeProfileFile(path string, cfg ProfileConfig) error {
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode profile: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create profile dir: %w", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("write profile: %w", err)
	}
	return nil
}

// profileNeedsLegacyConversion reports OG profiles (targets without a non-empty "builds" list).
func profileNeedsLegacyConversion(raw []byte) bool {
	var top struct {
		Targets []json.RawMessage `json:"targets"`
	}
	if err := json.Unmarshal(raw, &top); err != nil || len(top.Targets) == 0 {
		return false
	}
	for _, tr := range top.Targets {
		var row map[string]json.RawMessage
		if err := json.Unmarshal(tr, &row); err != nil {
			continue
		}
		braw, ok := row["builds"]
		if !ok {
			return true
		}
		var builds []json.RawMessage
		if err := json.Unmarshal(braw, &builds); err != nil || len(builds) == 0 {
			return true
		}
	}
	return false
}

type legacyTargetRow struct {
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Enabled  bool   `json:"enabled"`
	Filename string `json:"filename"`
	Package  string `json:"package"`
}

// convertLegacyProfileToCurrent rewrites OG / flat target rows into nested builds (exact filenames per os/arch).
func convertLegacyProfileToCurrent(raw []byte, workDir string) (ProfileConfig, error) {
	var old struct {
		Version   int                  `json:"version"`
		OutputDir string               `json:"output_dir"`
		Name      string               `json:"name"`
		Build     ProfileBuildSettings `json:"build"`
		Targets   []legacyTargetRow    `json:"targets"`
	}
	if err := json.Unmarshal(raw, &old); err != nil {
		return ProfileConfig{}, err
	}

	entries := mainPackageEntries(workDir)
	platforms := make([]ProfilePlatform, 0, len(old.Targets))

	type key struct{ os, arch string }
	byPlatform := make(map[key]*ProfilePlatform)
	order := []key{}

	for _, row := range old.Targets {
		k := key{os: row.OS, arch: row.Arch}
		plat, ok := byPlatform[k]
		if !ok {
			plat = &ProfilePlatform{OS: row.OS, Arch: row.Arch, Enabled: row.Enabled}
			byPlatform[k] = plat
			order = append(order, k)
		}
		t := targets.Target{OS: row.OS, Arch: row.Arch}
		pkg := row.Package
		if pkg == "" {
			pkg = "."
		}
		fn := row.Filename
		if fn == "" {
			for _, b := range buildsWithExactFilenames(entries, t, workDir) {
				plat.Builds = append(plat.Builds, b)
			}
			continue
		}
		base := fn
		plat.Builds = append(plat.Builds, ProfilePlatformBuild{
			Package:  normalizeProfilePackage(pkg),
			Filename: targets.OutputName(base, t),
		})
	}

	for _, k := range order {
		p := byPlatform[k]
		if len(p.Builds) == 0 {
			t := targets.Target{OS: p.OS, Arch: p.Arch}
			p.Builds = buildsWithExactFilenames(entries, t, workDir)
		}
		platforms = append(platforms, *p)
	}

	if len(platforms) == 0 {
		platforms = defaultPlatformTargets(workDir, true)
	}

	outDir := old.OutputDir
	if outDir == "" {
		outDir = "builds"
	}

	return ProfileConfig{
		Version:   profileVersion,
		OutputDir: outDir,
		Name:      old.Name,
		Build:     mergeProfileBuildSettings(old.Build, defaultProfileBuildSettings()),
		Release:   defaultProfileReleaseSettings(),
		Targets:   platforms,
	}, nil
}

func fillProfileDefaults(cfg *ProfileConfig) {
	if cfg == nil {
		return
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = "builds"
	}
	cfg.Build = mergeProfileBuildSettings(cfg.Build, defaultProfileBuildSettings())
	cfg.Release = mergeProfileReleaseSettings(cfg.Release, defaultProfileReleaseSettings())
}

func mergeProfileReleaseSettings(existing, defaults ProfileReleaseSettings) ProfileReleaseSettings {
	out := defaults
	if existing.Forge != "" {
		out.Forge = existing.Forge
	}
	out.APIURL = existing.APIURL
	out.Repo = existing.Repo
	out.Tag = existing.Tag
	out.Notes = existing.Notes
	out.NotesFile = existing.NotesFile
	out.UploadAfterBuild = existing.UploadAfterBuild
	out.CreateIfMissing = existing.CreateIfMissing
	out.Draft = existing.Draft
	return out
}

// loadAndApplyProfileFromFile applies builds/pbuild-profile.json when present (does not create it).
func loadAndApplyProfileFromFile(cmd *cobra.Command, workDir string) {
	path := filepath.Join(workDir, profileDirName, profileFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var cfg ProfileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return
	}
	fillProfileDefaults(&cfg)
	applyProfileConfig(cmd, cfg)
}

func mergeProfileBuildSettings(existing, defaults ProfileBuildSettings) ProfileBuildSettings {
	out := defaults
	if existing.Strategy != "" {
		out.Strategy = existing.Strategy
	}
	if existing.AMD64Level != "" {
		out.AMD64Level = existing.AMD64Level
	}
	if existing.ARM64Level != "" {
		out.ARM64Level = existing.ARM64Level
	}
	if existing.ARMLevel != "" {
		out.ARMLevel = existing.ARMLevel
	}
	if existing.MIPSLevel != "" {
		out.MIPSLevel = existing.MIPSLevel
	}
	if existing.PPC64Level != "" {
		out.PPC64Level = existing.PPC64Level
	}
	if existing.RISCVLevel != "" {
		out.RISCVLevel = existing.RISCVLevel
	}
	if existing.BuildMode != "" {
		out.BuildMode = existing.BuildMode
	}
	out.Tags = existing.Tags
	out.LDFlags = existing.LDFlags
	out.BuildFlags = existing.BuildFlags
	out.Compress = existing.Compress
	if existing.Parallel > 0 {
		out.Parallel = existing.Parallel
	}
	out.Verbose = existing.Verbose
	out.SkipCleanup = existing.SkipCleanup
	out.StopOnError = existing.StopOnError
	out.CleanCache = existing.CleanCache
	out.Checksums = existing.Checksums
	out.Reproducible = existing.Reproducible
	out.Vendor = existing.Vendor
	out.Sign = existing.Sign
	out.SigningKeyFile = existing.SigningKeyFile
	out.SigningKey = existing.SigningKey
	out.SignArmor = existing.SignArmor
	out.Provenance = existing.Provenance
	return out
}

func normalizeProfilePackage(path string) string {
	path = strings.TrimSpace(path)
	path = filepath.ToSlash(path)
	if path == "" {
		return "."
	}
	if !strings.HasPrefix(path, "./") && path != "." {
		path = "./" + path
	}
	return path
}

func applyProfileConfig(cmd *cobra.Command, cfg ProfileConfig) {
	if cmd == nil {
		return
	}
	applyStringFlag(cmd, "output-dir", &flagOutDir, cfg.OutputDir)
	applyStringFlag(cmd, "name", &flagName, cfg.Name)
	applyProfileBuildSettings(cmd, cfg.Build)
	applyProfileReleaseSettings(cmd, cfg.Release)
	profilePlatforms = append([]ProfilePlatform(nil), cfg.Targets...)
}

// profileArtifactName is the basename for go build -o under builds/<version>/.
func profileArtifactName(profileFilename, binaryName string, t targets.Target) string {
	if profileFilename != "" {
		return profileFilename
	}
	return targets.OutputName(binaryName, t)
}

func applyProfileBuildSettings(cmd *cobra.Command, b ProfileBuildSettings) {
	if cmd == nil {
		return
	}
	applyStringFlag(cmd, "strategy", &flagStrategy, b.Strategy)
	applyStringFlag(cmd, "amd64-level", &flagAMD64Level, b.AMD64Level)
	applyStringFlag(cmd, "arm64-level", &flagARM64Level, b.ARM64Level)
	applyStringFlag(cmd, "arm-level", &flagARMLevel, b.ARMLevel)
	applyStringFlag(cmd, "mips-level", &flagMIPSLevel, b.MIPSLevel)
	applyStringFlag(cmd, "ppc64-level", &flagPPC64Level, b.PPC64Level)
	applyStringFlag(cmd, "riscv-level", &flagRISCVLevel, b.RISCVLevel)
	applyStringFlag(cmd, "buildmode", &flagBuildMode, b.BuildMode)
	applyStringFlag(cmd, "tags", &flagTags, b.Tags)
	applyStringFlag(cmd, "ldflags", &flagLDFlags, b.LDFlags)
	applyStringFlag(cmd, "build-flags", &flagBuildFlags, b.BuildFlags)
	applyStringFlag(cmd, "compress", &flagCompress, b.Compress)
	applyStringFlag(cmd, "signing-key-file", &flagSigningKeyFile, b.SigningKeyFile)
	applyStringFlag(cmd, "signing-key", &flagSigningKey, b.SigningKey)

	if b.Parallel > 0 {
		applyIntFlag(cmd, "parallel", &flagParallel, b.Parallel)
	}
	applyBoolFlag(cmd, "verbose", &flagVerbose, b.Verbose)
	applyBoolFlag(cmd, "skip-cleanup", &flagSkipCleanup, b.SkipCleanup)
	applyBoolFlag(cmd, "stop-on-error", &flagStopOnError, b.StopOnError)
	applyBoolFlag(cmd, "clean-cache", &flagCleanCache, b.CleanCache)
	applyBoolFlag(cmd, "checksums", &flagChecksums, b.Checksums)
	applyBoolFlag(cmd, "reproducible", &flagReproducible, b.Reproducible)
	applyBoolFlag(cmd, "vendor", &flagVendor, b.Vendor)
	applyBoolFlag(cmd, "sign", &flagSign, b.Sign)
	applyBoolFlag(cmd, "sign-armor", &flagSignArmor, b.SignArmor)
	applyBoolFlag(cmd, "provenance", &flagProvenance, b.Provenance)
}

func applyProfileReleaseSettings(cmd *cobra.Command, r ProfileReleaseSettings) {
	if cmd == nil {
		return
	}
	applyStringFlag(cmd, "release-forge", &flagReleaseForge, r.Forge)
	applyStringFlag(cmd, "release-api-url", &flagReleaseAPIURL, r.APIURL)
	applyStringFlag(cmd, "release-repo", &flagReleaseRepo, r.Repo)
	applyStringFlag(cmd, "release-tag", &flagReleaseTag, r.Tag)
	applyStringFlag(cmd, "release-notes", &flagReleaseNotes, r.Notes)
	applyStringFlag(cmd, "release-notes-file", &flagReleaseNotesFile, r.NotesFile)
	applyBoolFlag(cmd, "upload-release", &flagUploadRelease, r.UploadAfterBuild)
	applyBoolFlag(cmd, "release-create", &flagReleaseCreate, r.CreateIfMissing)
	applyBoolFlag(cmd, "release-draft", &flagReleaseDraft, r.Draft)
}

func applyStringFlag(cmd *cobra.Command, name string, dst *string, value string) {
	if value == "" {
		return
	}
	if flagChanged(cmd, name) {
		return
	}
	*dst = value
}

func applyIntFlag(cmd *cobra.Command, name string, dst *int, value int) {
	if flagChanged(cmd, name) {
		return
	}
	*dst = value
}

func applyBoolFlag(cmd *cobra.Command, name string, dst *bool, value bool) {
	if flagChanged(cmd, name) {
		return
	}
	*dst = value
}

func flagChanged(cmd *cobra.Command, name string) bool {
	f := cmd.Flags().Lookup(name)
	return f != nil && f.Changed
}
