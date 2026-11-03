package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"pbuild/targets"
)

const (
	profileFilename = "pbuild-profile.json"
	profileDirName  = "builds" // profile always lives in the builds folder (not in version dir, not in custom output-dir)
)

const profileVersion = 2

// ProfileConfig is stored in builds/pbuild-profile.json when using --profile.
type ProfileConfig struct {
	Version int                  `json:"version"`
	Targets []ProfileTarget        `json:"targets"`
	Build   ProfileBuildSettings `json:"build"`
}

// ProfileTarget is one OS/arch in the profile.
type ProfileTarget struct {
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	Enabled bool   `json:"enabled"`
}

// ProfileBuildSettings holds build flags (same names as CLI / build-metadata.json).
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

func newDefaultProfileConfig() ProfileConfig {
	def := targets.Default()
	cfg := ProfileConfig{
		Version: profileVersion,
		Build:   defaultProfileBuildSettings(),
		Targets: make([]ProfileTarget, len(def)),
	}
	for i, t := range def {
		cfg.Targets[i] = ProfileTarget{OS: t.OS, Arch: t.Arch, Enabled: true}
	}
	return cfg
}

// loadProfile reads builds/pbuild-profile.json or creates it on first run.
func loadProfile(workDir string) (cfg ProfileConfig, matrix []targets.Target, justCreated bool, err error) {
	profileDir := filepath.Join(workDir, profileDirName)
	path := filepath.Join(profileDir, profileFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return ProfileConfig{}, nil, false, fmt.Errorf("read profile: %w", err)
		}
		cfg = newDefaultProfileConfig()
		body, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.MkdirAll(profileDir, 0o755); err != nil {
			return ProfileConfig{}, nil, false, fmt.Errorf("create builds dir for profile: %w", err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return ProfileConfig{}, nil, false, fmt.Errorf("write profile: %w", err)
		}
		for _, t := range cfg.Targets {
			if t.Enabled {
				matrix = append(matrix, targets.Target{OS: t.OS, Arch: t.Arch})
			}
		}
		return cfg, matrix, true, nil
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return ProfileConfig{}, nil, false, fmt.Errorf("parse profile: %w", err)
	}
	for _, t := range cfg.Targets {
		if t.Enabled {
			matrix = append(matrix, targets.Target{OS: t.OS, Arch: t.Arch})
		}
	}
	return cfg, matrix, false, nil
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
