package clitools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// detectTimeout caps how long a single tool's detection (config read + version
// probe) may run. Keeps one misbehaving CLI from stalling the whole batch.
const detectTimeout = 4 * time.Second

// versionProbeTimeout caps each `<cmd> --version` subprocess call.
const versionProbeTimeout = 2 * time.Second

// detectEnv holds the per-scan lookup state shared by every tool: the extra
// binary directories and the installed IDE extensions. Building it once per
// DetectAll keeps the directory globs and listings out of the per-tool path.
type detectEnv struct {
	binDirs    []string
	extensions []extension
}

func newDetectEnv(homeDir string) *detectEnv {
	return &detectEnv{
		binDirs:    extraBinDirs(homeDir),
		extensions: scanExtensions(homeDir),
	}
}

// ---- binaries ----------------------------------------------------------------

// lookupBinary resolves name to an executable path: PATH first, then the
// global bin dirs of common toolchains. A gateway launched from a desktop
// entry, systemd unit or launchd agent usually has a minimal PATH that lacks
// nvm/volta/pnpm/cargo, so without the fallback an installed CLI reads as
// missing.
//
// exec.LookPath is used for both passes: no subprocess, and on Windows it
// honors PATHEXT, so npm's .cmd shims resolve too.
func (e *detectEnv) lookupBinary(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	if p, err := exec.LookPath(name); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs, true
		}
		return p, true
	}
	for _, dir := range e.binDirs {
		if p, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			return p, true
		}
	}
	return "", false
}

// systemBinDirs controls whether machine-wide bin dirs (/usr/local/bin,
// Homebrew, snap) are searched. Tests turn it off so host installs cannot leak
// into detection results.
var systemBinDirs = true

// extraBinDirs returns global binary directories that are commonly missing
// from a service's PATH. Version-manager dirs are globbed newest-first.
func extraBinDirs(home string) []string {
	var dirs []string
	add := func(paths ...string) {
		for _, p := range paths {
			if p != "" {
				dirs = append(dirs, p)
			}
		}
	}
	glob := func(pattern string) {
		matches, _ := filepath.Glob(pattern)
		sort.Sort(sort.Reverse(sort.StringSlice(matches)))
		add(matches...)
	}
	envBin := func(name string, sub ...string) string {
		if v := os.Getenv(name); v != "" {
			return filepath.Join(append([]string{v}, sub...)...)
		}
		return ""
	}

	// Cross-platform, user-scoped locations.
	add(
		envBin("PNPM_HOME"),
		envBin("VOLTA_HOME", "bin"),
		envBin("BUN_INSTALL", "bin"),
		envBin("CARGO_HOME", "bin"),
		envBin("GOBIN"),
	)
	if home != "" {
		add(
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".claude", "local"), // Claude Code "local" installs
			filepath.Join(home, ".opencode", "bin"),
			filepath.Join(home, ".bun", "bin"),
			filepath.Join(home, ".cargo", "bin"),
			filepath.Join(home, ".deno", "bin"),
			filepath.Join(home, "go", "bin"),
			filepath.Join(home, ".volta", "bin"),
			filepath.Join(home, ".npm-global", "bin"),
			filepath.Join(home, ".npm-packages", "bin"),
			filepath.Join(home, ".yarn", "bin"),
			filepath.Join(home, ".asdf", "shims"),
			filepath.Join(home, ".local", "share", "mise", "shims"),
		)
		nvm := os.Getenv("NVM_DIR")
		if nvm == "" {
			nvm = filepath.Join(home, ".nvm")
		}
		glob(filepath.Join(nvm, "versions", "node", "*", "bin"))
		glob(filepath.Join(home, ".local", "share", "fnm", "node-versions", "*", "installation", "bin"))
	}
	if prefix := os.Getenv("NPM_CONFIG_PREFIX"); prefix != "" {
		if runtime.GOOS == "windows" {
			add(prefix)
		} else {
			add(filepath.Join(prefix, "bin"))
		}
	}

	switch runtime.GOOS {
	case "windows":
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			add(filepath.Join(appdata, "npm"), filepath.Join(appdata, "pnpm"))
		}
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			add(
				filepath.Join(local, "pnpm"),
				filepath.Join(local, "Volta", "bin"),
				filepath.Join(local, "Microsoft", "WinGet", "Links"),
				filepath.Join(local, "Programs", "claude"),
			)
		}
		if home != "" {
			add(filepath.Join(home, "scoop", "shims"))
		}
	case "darwin":
		if home != "" {
			add(filepath.Join(home, "Library", "pnpm"))
			glob(filepath.Join(home, "Library", "Application Support", "fnm", "node-versions", "*", "installation", "bin"))
		}
		if systemBinDirs {
			add("/opt/homebrew/bin", "/usr/local/bin")
		}
	default: // linux / *bsd
		if home != "" {
			add(filepath.Join(home, ".local", "share", "pnpm"))
		}
		if systemBinDirs {
			add("/usr/local/bin", "/home/linuxbrew/.linuxbrew/bin", "/snap/bin", "/usr/bin")
		}
	}
	return dirs
}

// ---- IDE extensions ----------------------------------------------------------

// extension is one installed VS Code-family extension folder.
type extension struct {
	id      string // lower-case "publisher.name"
	version string
	editor  string
}

func (e extension) label() string {
	s := e.id
	if e.version != "" {
		s += "@" + e.version
	}
	return s + " in " + e.editor
}

// extensionRoots lists the extension directories of VS Code-family editors.
func extensionRoots(home string) []struct{ editor, dir string } {
	if home == "" {
		return nil
	}
	return []struct{ editor, dir string }{
		{"VS Code", filepath.Join(home, ".vscode", "extensions")},
		{"VS Code Insiders", filepath.Join(home, ".vscode-insiders", "extensions")},
		{"VS Code Server", filepath.Join(home, ".vscode-server", "extensions")},
		{"Cursor", filepath.Join(home, ".cursor", "extensions")},
		{"Windsurf", filepath.Join(home, ".windsurf", "extensions")},
		{"VSCodium", filepath.Join(home, ".vscode-oss", "extensions")},
	}
}

// extFolderRe splits "publisher.name-1.2.3[-platform]" into id and version.
var extFolderRe = regexp.MustCompile(`^([a-z0-9][a-z0-9-]*\.[a-z0-9][a-z0-9._-]*?)-(\d+\.\d+\.\d+[0-9a-z.+-]*)$`)

// scanExtensions lists installed extensions across all known editors.
func scanExtensions(home string) []extension {
	var out []extension
	for _, root := range extensionRoots(home) {
		entries, err := os.ReadDir(root.dir)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if !ent.IsDir() {
				continue
			}
			m := extFolderRe.FindStringSubmatch(strings.ToLower(ent.Name()))
			if m == nil {
				continue
			}
			version := m[2]
			// Drop a platform suffix such as "-linux-x64".
			if i := strings.Index(version, "-"); i > 0 {
				version = version[:i]
			}
			out = append(out, extension{id: m[1], version: version, editor: root.editor})
		}
	}
	return out
}

// findExtension returns the newest installed extension matching any of ids.
func (e *detectEnv) findExtension(ids []string) (extension, bool) {
	var best extension
	found := false
	for _, want := range ids {
		want = strings.ToLower(want)
		for _, ext := range e.extensions {
			if ext.id == want && (!found || compareVersions(ext.version, best.version) > 0) {
				best, found = ext, true
			}
		}
		if found {
			return best, true
		}
	}
	return best, found
}

// compareVersions compares dotted numeric versions; non-numeric parts sort as 0.
func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			fmt.Sscanf(pa[i], "%d", &x)
		}
		if i < len(pb) {
			fmt.Sscanf(pb[i], "%d", &y)
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// ---- versions ----------------------------------------------------------------

// versionCache memoizes `--version` output per binary. Entries are keyed by
// path and invalidated when the file's size or mtime changes (an upgrade), so
// opening the page does not spawn a subprocess per tool every time.
type versionCache struct {
	mu      sync.Mutex
	entries map[string]versionEntry
}

type versionEntry struct {
	size    int64
	modTime time.Time
	version string
}

func (c *versionCache) get(ctx context.Context, bin string) string {
	info, err := os.Stat(bin)
	if err != nil {
		return ""
	}
	c.mu.Lock()
	if e, ok := c.entries[bin]; ok && e.size == info.Size() && e.modTime.Equal(info.ModTime()) {
		c.mu.Unlock()
		return e.version
	}
	c.mu.Unlock()

	v, ok := probeVersion(ctx, bin)
	if !ok {
		// Probe was cut short by the caller's deadline: do not cache, so the
		// next scan retries.
		return v
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[string]versionEntry)
	}
	c.entries[bin] = versionEntry{size: info.Size(), modTime: info.ModTime(), version: v}
	c.mu.Unlock()
	return v
}

// versionRe matches the first semver-ish token in version output.
var versionRe = regexp.MustCompile(`v?(\d+\.\d+(?:\.\d+)?(?:[-+][0-9A-Za-z.\-]+)?)`)

// ansiRe matches terminal escape sequences some CLIs print even when piped.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// probeVersion runs `<bin> --version`, falling back to `-v`, and extracts a
// version number. Only flags are tried: a bare `version` argument would be
// taken as a prompt by several agent CLIs. stdin is the null device so an
// interactive CLI cannot block on input, and the process is killed at the
// deadline. ok=false means the probe was interrupted by ctx.
func probeVersion(ctx context.Context, bin string) (string, bool) {
	for _, flag := range []string{"--version", "-v"} {
		pctx, cancel := context.WithTimeout(ctx, versionProbeTimeout)
		cmd := exec.CommandContext(pctx, bin, flag)
		cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")
		cmd.WaitDelay = 500 * time.Millisecond
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		err := cmd.Run()
		cancel()
		if ctx.Err() != nil {
			return "", false
		}
		if out.Len() == 0 && err != nil {
			continue
		}
		text := ansiRe.ReplaceAllString(out.String(), "")
		if m := versionRe.FindStringSubmatch(text); m != nil {
			return m[1], true
		}
	}
	return "", true
}
