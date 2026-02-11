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
- **Reproducible builds** (`--reproducible`): same code produces the same binary hash; forces `-trimpath` and deterministic gzip
- **Vendor support** (`--vendor`): build with `-mod=vendor`; creates `vendor/` if missing; prompts to re-vendor and retry when builds fail due to out-of-date vendored packages
- **GPG signing** (`--sign`): create detached signatures (`.sig` or `.asc`) for each artifact using a pure-Go OpenPGP implementation; each signature is verified after creation
- **Profile** (`--profile`): use a saved target list from `builds/pbuild-profile.json`; on first run the file is created with all targets enabled—edit it to set `"enabled": false` for targets you don't want; subsequent builds use only enabled targets

## Installation

```bash
go install github.com/earentir/pbuild@latest
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

Reproducible builds (same code → same binary hash; recommended with `--vendor` for fully deterministic builds):
```bash
pbuild --reproducible --vendor
```

Use vendored dependencies (creates `vendor/` on first run if missing; prompts to re-vendor on failure if packages are out of date):
```bash
pbuild --vendor
```

Sign release artifacts with GPG (requires a key file; see [Generating a GPG key pair](#generating-a-gpg-key-pair)):
```bash
pbuild --all --sign --signing-key-file ./release-key.asc
```

Use a profile to build only the targets you want (first run creates `builds/pbuild-profile.json` with all targets enabled; edit to disable, then run again):
```bash
pbuild --profile
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
      --reproducible         ensure reproducible builds (same code → same binary hash); forces -trimpath and deterministic gzip
      --riscv-level string   GORISCV64 level: rva20u64, rva22u64 (default "rva20u64")
      --skip-cleanup         skip cleaning previous build directory
      --stop-on-error        stop building others when one fails
      --strategy string      build strategy: flexible, purego, traditional (default "purego")
      --tags string          additional build tags (comma-separated)
      --verbose              show actual go build commands
      --vendor               use vendored dependencies (-mod=vendor); create vendor/ if missing; prompt to re-vendor on failure if out of date
      --sign                  create GPG detached signatures (.sig or .asc) for each artifact and verify after signing
      --signing-key-file path path to armored private key file (required when --sign)
      --signing-key id        key ID (hex) when key file contains multiple keys
      --sign-armor            output ASCII-armored signatures (.asc) instead of binary (.sig)
      --profile               use target list from builds/pbuild-profile.json (create on first run; edit to disable targets)
      --set-version string    override embedded version tag
```

## Profile (saved target list)

With `--profile`, pbuild uses a config file in the output directory to decide which OS/arch targets to build. This lets you avoid `--all` while still building a fixed set of targets every time.

1. **First run:** `pbuild --profile` creates `builds/pbuild-profile.json` with every predefined target and `"enabled": true`, then exits (no build). Edit the file, then run again.
2. **Edit the file:** Set `"enabled": false` for any target you don't want (e.g. drop `freebsd`, `openbsd`, or specific arches).
3. **Next runs:** `pbuild --profile` builds only the targets that still have `"enabled": true`.

Example profile (after editing):

```json
{
  "version": 1,
  "targets": [
    { "os": "linux", "arch": "amd64", "enabled": true },
    { "os": "linux", "arch": "arm64", "enabled": true },
    { "os": "darwin", "arch": "amd64", "enabled": false },
    { "os": "darwin", "arch": "arm64", "enabled": true },
    { "os": "windows", "arch": "amd64", "enabled": true }
  ]
}
```

The profile file lives under your output dir (default `builds/`), which is typically in `.gitignore`, so it is not committed. You can commit it if you want to share the same target list with others.

## Generating a GPG key pair

pbuild does **not** create or manage keys; it only signs with a key you provide. Use [GnuPG](https://gnupg.org/) to create a key and export it for pbuild.

### 1. Create a new key (GnuPG 2.1+)

```bash
gpg --full-generate-key
```

- Choose **RSA and RSA** (or **EdDSA** if you prefer).
- Set key size (e.g. 4096 for RSA).
- Enter your name, email, and an optional comment.
- Set a **passphrase** to protect the private key (recommended).

### 2. List your secret keys

```bash
gpg --list-secret-keys --keyid-format=long
```

Example output:

```
sec   rsa4096/ABCD1234EF567890 2024-01-15 [SC]
      XXXX...
uid                 [ultimate] Your Name <you@example.com>
```

The key ID is the part after the slash (e.g. `ABCD1234EF567890`).

### 3. Export the private key to a file (for pbuild)

Export the key in **armored** form so pbuild can read it:

```bash
gpg --export-secret-keys -a KEY_ID > release-key.asc
```

Replace `KEY_ID` with your key ID (e.g. `ABCD1234EF567890`). Keep `release-key.asc` secure and **do not commit it** to version control. Add it to `.gitignore`:

```
release-key.asc
```

### 4. Use the key with pbuild

```bash
pbuild --all --sign --signing-key-file ./release-key.asc
```

- If the key is **passphrase-protected**, pbuild will prompt for the passphrase, or you can set `PBUILD_SIGNING_PASSPHRASE` in the environment (e.g. in CI).
- If the key file contains **multiple keys**, specify which one with `--signing-key KEY_ID`.
- Use `--sign-armor` to produce `.asc` (ASCII) signatures instead of binary `.sig`.

### 5. Verify signatures (downstream users)

Anyone can verify a signed artifact with GnuPG:

```bash
gpg --verify myapp.sig myapp
```

Import your **public** key first if needed:

```bash
gpg --import your-public-key.asc
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
    ├── myapp.sig           # GPG detached signatures (if --sign used; .asc with --sign-armor)
    └── build-metadata.json # Build information and configuration
```

## .gitignore Management

The tool automatically manages the `builds/` directory in your `.gitignore` file:

- **If `.gitignore` exists but lacks `builds/`**: Adds the entry
- **If `.gitignore` doesn't exist**: Skips the check (doesn't create the file)
- **If `builds/` already exists**: Confirms it's present

This ensures your build artifacts are properly ignored by git without cluttering your repository.