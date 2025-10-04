# pbuild

Build go projects, it is very specific to my project but can be used by anyone.

A cross-compilation tool for Go projects that builds for multiple target platforms with automatic `.gitignore` management.

## Features

- Cross-compile Go projects for multiple platforms
- Automatic `.gitignore` management (adds `builds/` directory if missing)
- Parallel builds with configurable workers
- Compression support (gzip, zstd)
- Checksum generation (SHA256, SHA512)
- Build metadata and reporting
- Flexible build strategies (purego, flexible, traditional)

## Installation

```bash
go install github.com/yourusername/pbuild@latest
```

## Usage

### Basic Usage

Build for current platform:
```bash
pbuild
```

Build for all predefined targets:
```bash
pbuild --all
```

Build with verbose output:
```bash
pbuild --verbose
```

### Example Runs

#### 1. Basic Build (Current Platform)
```bash
$ pbuild
builds/ directory already in .gitignore file
Building version 1.1.7-abc123

 BUILD CONFIG │ VALUE     CPU LEVELS │   VALUE           BEHAVIOR      │ VALUE 
──────────────┼────────  ────────────┼───────────  ────────────────────┼───────
 Strategy     │ purego    AMD64      │ v2           Parallel Workers   │ 6     
──────────────┼────────  ────────────┼───────────  ────────────────────┼───────
 Build Mode   │ auto      ARM64      │ v8.0         Clean Cache        │ false 
                         ────────────┼───────────  ────────────────────┼───────
                          ARM        │ 7            Skip Cleanup       │ false 
                         ────────────┼───────────  ────────────────────┼───────
                          MIPS       │ hardfloat    Stop on Error      │ false 
                         ────────────┼───────────  ────────────────────┼───────
                          PPC64      │ power8       Verbose            │ false 
                         ────────────┼───────────  ────────────────────┼───────
                          RISC-V     │ rva20u64     Generate Checksums │ true  

Building for: linux/amd64 -> /path/to/project/builds/1.1.7-abc123/myapp
  SUCCESS

Artifacts for myapp, version 1.1.7-abc123
stored in /path/to/project/builds/1.1.7-abc123

  FILE  │   TARGET    │       SIZE        │                             SHA 256                              │ STATUS 
────────┼─────────────┼───────────────────┼──────────────────────────────────────────────────────────────────┼────────
 myapp  │ linux/amd64 │ 2.1 MiB (2201234) │ a1b2c3d4e5f6789012345678901234567890abcdef1234567890abcdef123456 │  ✓      

Build summary: Total: 1  Success: 1  Failed: 0

Build metadata written to: /path/to/project/builds/1.1.7-abc123/build-metadata.json
```

#### 2. Cross-Platform Build (All Targets)
```bash
$ pbuild --all
builds/ directory already in .gitignore file
Building version 1.1.7-abc123

[Configuration tables shown above]

Building for: linux/amd64 -> /path/to/project/builds/1.1.7-abc123/myapp
  SUCCESS

Building for: linux/arm64 -> /path/to/project/builds/1.1.7-abc123/myapp
  SUCCESS

Building for: windows/amd64 -> /path/to/project/builds/1.1.7-abc123/myapp.exe
  SUCCESS

Building for: darwin/amd64 -> /path/to/project/builds/1.1.7-abc123/myapp
  SUCCESS

Building for: darwin/arm64 -> /path/to/project/builds/1.1.7-abc123/myapp
  SUCCESS

Artifacts for myapp, version 1.1.7-abc123
stored in /path/to/project/builds/1.1.7-abc123

  FILE      │   TARGET    │       SIZE        │                             SHA 256                              │ STATUS 
────────────┼─────────────┼───────────────────┼──────────────────────────────────────────────────────────────────┼────────
 myapp      │ linux/amd64 │ 2.1 MiB (2201234) │ a1b2c3d4e5f6789012345678901234567890abcdef1234567890abcdef123456 │  ✓      
 myapp      │ linux/arm64 │ 1.8 MiB (1887654) │ b2c3d4e5f6789012345678901234567890abcdef1234567890abcdef1234567a │  ✓      
 myapp.exe  │ windows/amd64│ 2.2 MiB (2309876) │ c3d4e5f6789012345678901234567890abcdef1234567890abcdef1234567ab2 │  ✓      
 myapp      │ darwin/amd64 │ 2.0 MiB (2105432) │ d4e5f6789012345678901234567890abcdef1234567890abcdef1234567abc3 │  ✓      
 myapp      │ darwin/arm64 │ 1.7 MiB (1765432) │ e5f6789012345678901234567890abcdef1234567890abcdef1234567abcd4 │  ✓      

Build summary: Total: 5  Success: 5  Failed: 0

Build metadata written to: /path/to/project/builds/1.1.7-abc123/build-metadata.json
```

#### 3. Verbose Build with Compression
```bash
$ pbuild --all --verbose --compress zstd
builds/ directory already in .gitignore file
Building version 1.1.7-abc123

[Configuration tables shown above]

[Worker 0] Building for: linux/amd64 -> /path/to/project/builds/1.1.7-abc123/myapp
  Command: go build -trimpath -buildmode=exe -tags purego -ldflags -s -w -X main.appVersion=1.1.7-abc123 -o /path/to/project/builds/1.1.7-abc123/myapp .
  Environment: GOOS=linux GOARCH=amd64 GOAMD64=v2
[Worker 0]   SUCCESS
[Worker 0]   Compressed to /path/to/project/builds/1.1.7-abc123/myapp.zst

[Worker 1] Building for: linux/arm64 -> /path/to/project/builds/1.1.7-abc123/myapp
  Command: go build -trimpath -buildmode=exe -tags purego -ldflags -s -w -X main.appVersion=1.1.7-abc123 -o /path/to/project/builds/1.1.7-abc123/myapp .
  Environment: GOOS=linux GOARCH=arm64 GOARM64=v8.0
[Worker 1]   SUCCESS
[Worker 1]   Compressed to /path/to/project/builds/1.1.7-abc123/myapp.zst

[Additional workers...]

Artifacts for myapp, version 1.1.7-abc123
stored in /path/to/project/builds/1.1.7-abc123

  FILE      │   TARGET    │       SIZE        │                             SHA 256                              │ STATUS 
────────────┼─────────────┼───────────────────┼──────────────────────────────────────────────────────────────────┼────────
 myapp.zst  │ linux/amd64 │ 890 KiB (911234)  │ f6789012345678901234567890abcdef1234567890abcdef1234567abcde5 │  ✓      
 myapp.zst  │ linux/arm64 │ 756 KiB (774321)  │ 789012345678901234567890abcdef1234567890abcdef1234567abcdef6 │  ✓      
 myapp.zst  │ windows/amd64│ 912 KiB (934567)  │ 89012345678901234567890abcdef1234567890abcdef1234567abcdef78 │  ✓      
 myapp.zst  │ darwin/amd64 │ 845 KiB (865432)  │ 9012345678901234567890abcdef1234567890abcdef1234567abcdef789 │  ✓      
 myapp.zst  │ darwin/arm64 │ 723 KiB (740123)  │ 012345678901234567890abcdef1234567890abcdef1234567abcdef7890 │  ✓      

Build summary: Total: 5  Success: 5  Failed: 0

Build metadata written to: /path/to/project/builds/1.1.7-abc123/build-metadata.json
```

#### 4. .gitignore Management Examples

When `.gitignore` doesn't exist:
```bash
$ pbuild
No .gitignore file found - skipping builds/ directory check
Building version 1.1.7-abc123
[... rest of build output ...]
```

When `builds/` is missing from existing `.gitignore`:
```bash
$ pbuild
Added builds/ to .gitignore file
Building version 1.1.7-abc123
[... rest of build output ...]
```

When `builds/` already exists in `.gitignore`:
```bash
$ pbuild
builds/ directory already in .gitignore file
Building version 1.1.7-abc123
[... rest of build output ...]
```

## Command Line Options

```bash
pbuild [TARGET_DIR] [flags]

Flags:
      --all                  build for all predefined targets
      --amd64-level string   GOAMD64 level: v1, v2, v3, v4 (default "v2")
      --arm-level string     GOARM level: 5, 6, 7 (default "7")
      --arm64-level string   GOARM64 level: v8.0, v8.1, v8.2, v8.3, v8.4, v8.5, v8.6, v8.7, v8.8, v8.9, v9.0, v9.1, v9.2, v9.3, v9.4, v9.5 (default "v8.0")
      --build-flags string   additional go build flags (default: -trimpath)
      --buildmode string     build mode: auto (exe), pie (requires CGO), exe, c-archive, c-shared (default "auto")
      --checksums            generate SHA256 and SHA512 checksums (default true)
      --clean-cache          clean Go build cache before building
      --compress string      compress binaries: zstd, gzip
      --ldflags string       custom ldflags (default: -s -w -X main.appVersion)
      --mips-level string    GOMIPS level: hardfloat, softfloat (default "hardfloat")
      --name string          override inferred project name
      --output-dir string    directory for build artifacts (default "builds")
      --parallel int         number of parallel builds (0 = sequential) (default 6)
      --ppc64-level string   GOPPC64 level: power8, power9, power10 (default "power8")
      --riscv-level string   GORISCV64 level: rva20u64, rva22u64 (default "rva20u64")
      --skip-cleanup         skip cleaning previous build directory
      --stop-on-error        stop building others when one fails
      --strategy string      build strategy: flexible, purego, traditional (default "purego")
      --tags string          additional build tags (comma-separated)
      --verbose              show actual go build commands
      --version string       override embedded version tag
```

## Build Artifacts

The tool creates a structured output directory:

```
builds/
└── 1.1.7-abc123/           # Version-specific directory
    ├── myapp               # Linux/Unix binaries
    ├── myapp.exe           # Windows binaries
    ├── myapp.zst           # Compressed binaries (if --compress used)
    ├── myapp.hash          # Checksum files (if --checksums enabled)
    └── build-metadata.json # Build information and configuration
```

## .gitignore Management

The tool automatically manages the `builds/` directory in your `.gitignore` file:

- **If `.gitignore` exists but lacks `builds/`**: Adds the entry
- **If `.gitignore` doesn't exist**: Skips the check (doesn't create the file)
- **If `builds/` already exists**: Confirms it's present

This ensures your build artifacts are properly ignored by git without cluttering your repository.