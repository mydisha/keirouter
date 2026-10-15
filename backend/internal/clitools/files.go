package clitools

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// backupSuffix is appended to a config file's path to name the copy taken
// before KeiRouter first edits it.
const backupSuffix = ".keirouter.bak"

// ParseError reports a config file that exists but cannot be parsed. Configure
// and Remove refuse to touch such a file: rewriting it from an empty document
// would silently discard every setting the user had in it.
type ParseError struct {
	Path string
	Err  error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%s could not be parsed (%v); fix or move the file, then try again", e.Path, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

// ---- reading -----------------------------------------------------------------

// readFile returns the file contents and whether it exists. A missing file is
// not an error.
func readFile(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	return data, true, nil
}

// isBlank reports whether data holds nothing but whitespace (or a BOM). Tools
// sometimes leave an empty config behind; it parses as an empty document.
func isBlank(data []byte) bool {
	return len(bytes.TrimSpace(bytes.TrimPrefix(data, utf8BOM))) == 0
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// loadJSON parses a JSON/JSONC file into v. It returns exists=false (and
// leaves v untouched) when the file is missing, and a *ParseError when the
// file exists but is not valid JSON.
func loadJSON(path string, v any) (bool, error) {
	data, exists, err := readFile(path)
	if err != nil || !exists || isBlank(data) {
		return exists, err
	}
	if err := json.Unmarshal(stripJSONC(data), v); err != nil {
		return true, &ParseError{Path: path, Err: err}
	}
	return true, nil
}

// loadJSONObject is loadJSON for the common object-rooted config. The returned
// map is never nil.
func loadJSONObject(path string) (map[string]any, bool, error) {
	cfg := map[string]any{}
	exists, err := loadJSON(path, &cfg)
	if cfg == nil { // file contained `null`
		cfg = map[string]any{}
	}
	return cfg, exists, err
}

// loadTOML parses a TOML file into a map (never nil).
func loadTOML(path string) (map[string]any, bool, error) {
	cfg := map[string]any{}
	data, exists, err := readFile(path)
	if err != nil || !exists || isBlank(data) {
		return cfg, exists, err
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, true, &ParseError{Path: path, Err: err}
	}
	return cfg, true, nil
}

// loadYAML parses a YAML file into a map (never nil).
func loadYAML(path string) (map[string]any, bool, error) {
	cfg := map[string]any{}
	data, exists, err := readFile(path)
	if err != nil || !exists || isBlank(data) {
		return cfg, exists, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, true, &ParseError{Path: path, Err: err}
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	return cfg, true, nil
}

// stripJSONC turns JSONC (the dialect VS Code, Claude Code and OpenCode accept)
// into strict JSON: it drops a UTF-8 BOM, // and /* */ comments, and trailing
// commas before } or ]. String literals are left intact, so URLs containing
// "//" survive.
func stripJSONC(data []byte) []byte {
	data = bytes.TrimPrefix(data, utf8BOM)
	out := make([]byte, 0, len(data))
	inStr, esc := false, false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inStr {
			out = append(out, c)
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch {
		case c == '"':
			inStr = true
			out = append(out, c)
		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			for i+1 < len(data) && data[i+1] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(data) && data[i+1] == '*':
			end := bytes.Index(data[i+2:], []byte("*/"))
			if end < 0 {
				i = len(data)
			} else {
				i += 2 + end + 1
			}
			out = append(out, ' ')
		default:
			out = append(out, c)
		}
	}
	return stripTrailingCommas(out)
}

// stripTrailingCommas removes commas whose next non-space byte closes an object
// or array. It skips string literals so a "," inside a value is never touched.
func stripTrailingCommas(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inStr, esc := false, false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			out = append(out, c)
			continue
		}
		if c == '"' {
			inStr = true
		}
		if c == ',' {
			j := i + 1
			for j < len(data) && (data[j] == ' ' || data[j] == '\t' || data[j] == '\n' || data[j] == '\r') {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// ---- writing -----------------------------------------------------------------

// saveJSON writes v as indented JSON.
func saveJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'))
}

// saveTOML writes cfg as TOML.
func saveTOML(path string, cfg map[string]any) error {
	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

// saveYAML writes cfg as YAML.
func saveYAML(path string, cfg map[string]any) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

// saveOrRemove writes cfg, or deletes the file when cfg has become empty —
// used by Remove so a file KeiRouter created does not linger as "{}".
func saveOrRemove(path string, cfg map[string]any, save func(string, map[string]any) error) error {
	if len(cfg) == 0 {
		return removeFile(path)
	}
	return save(path, cfg)
}

func saveJSONObject(path string, cfg map[string]any) error { return saveJSON(path, cfg) }

// writeFileAtomic replaces path with data via a temp file + rename, so a crash
// or a concurrent reader never sees a half-written config. An existing file's
// permission bits are kept; new files are 0600 because most of these configs
// hold API keys.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	perm := fs.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// removeFile deletes path, treating "already gone" as success.
func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// fileExists reports whether path exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ---- backups -----------------------------------------------------------------

// backupPath returns where the pre-KeiRouter copy of path is kept.
func backupPath(path string) string { return path + backupSuffix }

// backupFile copies path to its backup location, replacing an older backup.
// A missing source is a no-op. Comments in JSONC/TOML/YAML files do not
// survive a structured rewrite, so this copy is the user's way back to the
// exact original.
func backupFile(path string) error {
	data, exists, err := readFile(path)
	if err != nil || !exists {
		return err
	}
	return writeFileAtomic(backupPath(path), data)
}

// ---- env files ---------------------------------------------------------------

// readEnvValue returns the value of key in a KEY=value env file ("" when the
// file or key is missing). Surrounding quotes are removed.
func readEnvValue(path, key string) (string, error) {
	data, exists, err := readFile(path)
	if err != nil || !exists {
		return "", err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		if k, v, ok := parseEnvLine(sc.Text()); ok && k == key {
			return v, nil
		}
	}
	return "", sc.Err()
}

// setEnvValue sets key=value in an env file, keeping every other line. An
// empty value removes the key; a file left with no content is deleted.
func setEnvValue(path, key, value string) error {
	data, _, err := readFile(path)
	if err != nil {
		return err
	}
	var lines []string
	replaced := false
	if len(data) > 0 {
		for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			if k, _, ok := parseEnvLine(line); ok && k == key {
				if value != "" && !replaced {
					lines = append(lines, key+"="+value)
					replaced = true
				}
				continue
			}
			lines = append(lines, line)
		}
	}
	if value != "" && !replaced {
		lines = append(lines, key+"="+value)
	}
	if strings.TrimSpace(strings.Join(lines, "")) == "" {
		return removeFile(path)
	}
	return writeFileAtomic(path, []byte(strings.Join(lines, "\n")+"\n"))
}

// parseEnvLine parses `KEY=value` / `export KEY="value"`. Comments and blank
// lines report ok=false.
func parseEnvLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimPrefix(line, "export ")
	k, v, found := strings.Cut(line, "=")
	if !found {
		return "", "", false
	}
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		v = v[1 : len(v)-1]
	}
	return strings.TrimSpace(k), v, true
}

// ---- map helpers -------------------------------------------------------------

// child returns cfg[key] as a map, creating (and attaching) it when absent or
// of the wrong type.
func child(cfg map[string]any, key string) map[string]any {
	if m, ok := cfg[key].(map[string]any); ok {
		return m
	}
	m := map[string]any{}
	cfg[key] = m
	return m
}

// lookup walks nested maps and returns the value at path, or nil.
func lookup(cfg map[string]any, path ...string) any {
	var cur any = cfg
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[p]
	}
	return cur
}

// str returns the string at path, or "".
func str(cfg map[string]any, path ...string) string {
	s, _ := lookup(cfg, path...).(string)
	return s
}

// pruneEmpty deletes cfg[key] when it is an empty map.
func pruneEmpty(cfg map[string]any, key string) {
	if m, ok := cfg[key].(map[string]any); ok && len(m) == 0 {
		delete(cfg, key)
	}
}
