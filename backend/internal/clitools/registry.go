// Package clitools implements auto-configuration for coding CLI tools. Each
// tool knows where its native config lives, how to read the gateway connection
// currently stored there, how to write KeiRouter settings into it and how to
// take them out again.
//
// The common strategy is merge-not-overwrite: existing settings are preserved;
// only KeiRouter-specific keys are added or removed. Before the first edit the
// original file is copied to "<file>.keirouter.bak", and a file that exists but
// cannot be parsed is never rewritten.
package clitools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// Settings is the gateway connection stored in a tool's config.
type Settings struct {
	// BaseURL is the gateway root without a trailing "/v1" — tools that need
	// the OpenAI-style "/v1" suffix add it themselves.
	BaseURL string `json:"base_url"`
	// APIKey is the plaintext credential. It is never serialized; callers
	// expose a masked hint instead.
	APIKey string `json:"-"`
	// Model is the tool's default model, "" when the tool picks its own.
	Model string `json:"model,omitempty"`
}

// Tool describes a CLI tool that can be auto-configured to use KeiRouter.
type Tool interface {
	// ID returns the tool's short identifier (e.g. "claude", "codex").
	ID() string
	// Name returns the human-readable display name.
	Name() string
	// Commands lists binary names that indicate the tool is installed, in
	// preference order. Empty for tools that ship only as IDE extensions.
	Commands() []string
	// Extensions lists VS Code-family extension ids ("publisher.name") whose
	// presence indicates the tool is installed.
	Extensions() []string
	// ConfigFiles lists every file Configure may write. The first entry is the
	// primary config shown in the UI; its existence is an install signal.
	ConfigFiles(homeDir string) []string
	// Read returns the connection currently stored in the tool's config: nil
	// when none is configured, an error (usually *ParseError) when a config
	// file exists but cannot be read.
	Read(homeDir string) (*Settings, error)
	// Configure writes s into the tool's config file(s). s.BaseURL is already
	// normalized to the gateway root and s.APIKey is non-empty.
	Configure(homeDir string, s Settings) error
	// Remove strips KeiRouter settings from the tool's config file(s).
	Remove(homeDir string) error
	// ModelRequired reports whether the tool needs a default model to work
	// (most custom-provider configs are unusable without one).
	ModelRequired() bool
}

// Status is the detection result for one tool.
type Status struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	// DetectedVia lists the install signals that matched: "binary",
	// "extension" and/or "config".
	DetectedVia []string `json:"detected_via,omitempty"`
	// Configured reports whether the tool's config points at a gateway.
	// Whether it is *this* gateway is decided by the caller from Settings.
	Configured bool `json:"configured"`
	// ConfigPath is the primary config file; ConfigFiles lists all of them.
	ConfigPath  string   `json:"config_path"`
	ConfigFiles []string `json:"config_files,omitempty"`
	// BackupPath is the pre-KeiRouter copy of the primary config, when one
	// exists.
	BackupPath string `json:"backup_path,omitempty"`
	// BinaryPath is the resolved executable path, "" when none was found.
	BinaryPath string `json:"binary_path,omitempty"`
	// Extension is the installed IDE extension ("publisher.name@version in
	// Editor"), "" when none was found.
	Extension string `json:"extension,omitempty"`
	// Version is the best-effort version reported by the binary or extension.
	Version string `json:"version,omitempty"`
	// ModelRequired mirrors Tool.ModelRequired for the UI.
	ModelRequired bool `json:"model_required"`
	// Settings is the connection currently stored in the config.
	Settings *Settings `json:"settings,omitempty"`
	// Error holds a detection error (unreadable config, timeout) — distinct
	// from the tool simply being absent.
	Error string `json:"error,omitempty"`
}

// Registry holds all known CLI tools.
type Registry struct {
	tools    map[string]Tool
	order    []string
	versions versionCache
	// mu serializes config writes so two concurrent Configure/Remove calls
	// cannot interleave their read-modify-write cycles.
	mu sync.Mutex
}

// NewRegistry builds a registry with all built-in tool implementations.
func NewRegistry() *Registry {
	r := &Registry{tools: make(map[string]Tool)}
	for _, t := range []Tool{
		&ClaudeTool{},
		&CodexTool{},
		&ClineTool{},
		&CopilotTool{},
		&DroidTool{},
		&OpenClawTool{},
		&OpenCodeTool{},
		&KiloTool{},
		&HermesTool{},
		&DeepSeekTool{},
		&JcodeTool{},
	} {
		r.tools[t.ID()] = t
		r.order = append(r.order, t.ID())
	}
	return r
}

// Get returns a tool by id, or nil if unknown.
func (r *Registry) Get(id string) Tool { return r.tools[id] }

// All returns all registered tools in registration order.
func (r *Registry) All() []Tool {
	out := make([]Tool, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.tools[id])
	}
	return out
}

// DetectAll returns the status of every registered tool. Detection runs in
// parallel with a per-tool timeout so one slow or misbehaving CLI cannot stall
// the batch. For each tool, detection combines:
//   - a binary lookup over PATH plus the global bin dirs of common Node,
//     Rust, Go and Python toolchains (a GUI- or service-launched gateway
//     often inherits a minimal PATH);
//   - an IDE extension lookup across VS Code, Insiders, Cursor, Windsurf and
//     VSCodium;
//   - the tool's own config file, parsed to read the stored connection; and
//   - a cached `<cmd> --version` probe.
func (r *Registry) DetectAll(ctx context.Context, homeDir string) []Status {
	env := newDetectEnv(homeDir)
	tools := r.All()
	out := make([]Status, len(tools))
	var wg sync.WaitGroup
	for i, t := range tools {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = r.detect(ctx, t, homeDir, env)
		}()
	}
	wg.Wait()
	return out
}

// Detect returns the status of a single tool.
func (r *Registry) Detect(ctx context.Context, t Tool, homeDir string) Status {
	return r.detect(ctx, t, homeDir, newDetectEnv(homeDir))
}

// Configure normalizes s, backs up the tool's config files on first use and
// writes the settings. It returns the tool's status after the write so callers
// can confirm what the file now holds.
func (r *Registry) Configure(ctx context.Context, t Tool, homeDir string, s Settings) (Status, error) {
	s.BaseURL = RootURL(s.BaseURL)
	s.APIKey = strings.TrimSpace(s.APIKey)
	s.Model = strings.TrimSpace(s.Model)
	if s.BaseURL == "" {
		return Status{}, fmt.Errorf("base URL is required")
	}
	if s.APIKey == "" {
		return Status{}, fmt.Errorf("API key is required")
	}
	if s.Model == "" && t.ModelRequired() {
		return Status{}, fmt.Errorf("%s needs a default model", t.Name())
	}

	r.mu.Lock()
	err := func() error {
		defer r.mu.Unlock()
		// Fail before touching anything when the current config is
		// unparseable — Configure would otherwise rebuild it from scratch.
		current, err := t.Read(homeDir)
		if err != nil {
			return err
		}
		// Snapshot the originals the first time KeiRouter takes over, or
		// whenever the tool is not currently connected (the user may have
		// edited the file since the last snapshot).
		for _, p := range t.ConfigFiles(homeDir) {
			if current == nil || !fileExists(backupPath(p)) {
				if err := backupFile(p); err != nil {
					return fmt.Errorf("backing up %s: %w", p, err)
				}
			}
		}
		return t.Configure(homeDir, s)
	}()
	if err != nil {
		return Status{}, err
	}
	return r.Detect(ctx, t, homeDir), nil
}

// Remove strips KeiRouter settings from the tool and returns its new status.
func (r *Registry) Remove(ctx context.Context, t Tool, homeDir string) (Status, error) {
	r.mu.Lock()
	err := t.Remove(homeDir)
	r.mu.Unlock()
	if err != nil {
		return Status{}, err
	}
	return r.Detect(ctx, t, homeDir), nil
}

// detect runs the full detection pipeline for a single tool. Failures are
// surfaced via Status.Error so the UI can tell "not installed" from
// "detection broke".
func (r *Registry) detect(ctx context.Context, t Tool, homeDir string, env *detectEnv) Status {
	ctx, cancel := context.WithTimeout(ctx, detectTimeout)
	defer cancel()

	st := Status{
		ID:            t.ID(),
		Name:          t.Name(),
		ConfigFiles:   t.ConfigFiles(homeDir),
		ModelRequired: t.ModelRequired(),
	}
	if len(st.ConfigFiles) > 0 {
		st.ConfigPath = st.ConfigFiles[0]
		if fileExists(st.ConfigPath) {
			st.DetectedVia = append(st.DetectedVia, "config")
		}
		if bp := backupPath(st.ConfigPath); fileExists(bp) {
			st.BackupPath = bp
		}
	}

	for _, cmd := range t.Commands() {
		if bin, ok := env.lookupBinary(cmd); ok {
			st.BinaryPath = bin
			st.DetectedVia = append([]string{"binary"}, st.DetectedVia...)
			break
		}
	}
	var extVersion string
	if ext, ok := env.findExtension(t.Extensions()); ok {
		st.Extension = ext.label()
		extVersion = ext.version
		st.DetectedVia = append(st.DetectedVia, "extension")
	}
	st.Installed = len(st.DetectedVia) > 0

	// Read the stored connection. Config reads are local and fast, but run
	// them under the timeout anyway so a hung network filesystem cannot stall
	// the batch. The channel is buffered, so a late result is simply dropped.
	type readResult struct {
		s   *Settings
		err error
	}
	ch := make(chan readResult, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				ch <- readResult{err: fmt.Errorf("reading config panicked: %v", p)}
			}
		}()
		s, err := t.Read(homeDir)
		ch <- readResult{s, err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			st.Error = res.err.Error()
		} else if res.s != nil && res.s.BaseURL != "" {
			res.s.BaseURL = RootURL(res.s.BaseURL)
			st.Settings = res.s
			st.Configured = true
		}
	case <-ctx.Done():
		st.Error = "reading config timed out"
	}

	if st.BinaryPath != "" {
		st.Version = r.versions.get(ctx, st.BinaryPath)
	}
	if st.Version == "" {
		st.Version = extVersion
	}
	return st
}

// ---- URL helpers -------------------------------------------------------------

// RootURL trims whitespace, trailing slashes and a trailing "/v1" so every
// tool starts from the same gateway root.
func RootURL(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	u = strings.TrimSuffix(u, "/v1")
	return strings.TrimRight(u, "/")
}

// v1URL returns the gateway's OpenAI-compatible base ("<root>/v1").
func v1URL(root string) string { return RootURL(root) + "/v1" }

// ---- path helpers ------------------------------------------------------------

// expandHome resolves ~ to homeDir.
func expandHome(homeDir, path string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(homeDir, filepath.FromSlash(path[2:]))
	}
	return path
}

// envDir returns $name when set (made absolute against homeDir), else the
// fallback path under homeDir. Several tools honor such an override
// (CODEX_HOME, XDG_CONFIG_HOME).
func envDir(homeDir, name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		if !filepath.IsAbs(v) {
			v = filepath.Join(homeDir, v)
		}
		return v
	}
	return expandHome(homeDir, fallback)
}

// vscodeUserDir returns the platform-specific VS Code User config directory.
func vscodeUserDir(homeDir string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir, "Library", "Application Support", "Code", "User")
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "Code", "User")
		}
		return filepath.Join(homeDir, "AppData", "Roaming", "Code", "User")
	default:
		return filepath.Join(envDir(homeDir, "XDG_CONFIG_HOME", "~/.config"), "Code", "User")
	}
}

// sortedKeys returns m's keys in sorted order (deterministic model lists).
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// envDirFile joins file onto envDir(homeDir, name, fallback).
func envDirFile(homeDir, name, fallback, file string) string {
	return filepath.Join(envDir(homeDir, name, fallback), file)
}
