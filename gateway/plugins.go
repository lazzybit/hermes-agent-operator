package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type pluginSource struct {
	Name       string `json:"name"`
	Repository string `json:"repository"`
	Revision   string `json:"revision"`
	Path       string `json:"path,omitempty"`
}

// applyPlugins reads an optional plugins.json. A missing file or an empty
// list does nothing. Fetched trees are written only under pluginsDir, which
// the pod mounts as an emptyDir, and the home plugins entry points at that
// directory. Plugin source is not copied onto the retained home volume.
func applyPlugins(profile, home, manifest, pluginsDir, depsDir string) error {
	if manifest == "" {
		return nil
	}
	data, err := os.ReadFile(manifest)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil
	}
	sources, err := loadPlugins(data, profile)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return nil
	}
	if pluginsDir == "" || depsDir == "" {
		return fmt.Errorf("plugins.json requires plugin projection directories")
	}
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(depsDir, 0o755); err != nil {
		return err
	}
	for _, src := range sources {
		dest := filepath.Join(pluginsDir, src.Name)
		if err := os.RemoveAll(dest); err != nil {
			return err
		}
		if err := fetchPlugin(src.Repository, src.Revision, src.Path, dest); err != nil {
			return fmt.Errorf("plugin %s: %w", src.Name, err)
		}
		if err := preparePython(dest, depsDir); err != nil {
			return fmt.Errorf("plugin %s: %w", src.Name, err)
		}
		fmt.Fprintf(os.Stderr, "plugins: prepared %s@%s\n", src.Name, src.Revision)
	}
	return pointPlugins(home, pluginsDir)
}

func loadPlugins(data []byte, profile string) ([]pluginSource, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '[' {
		return parsePlugins(data)
	}
	var doc struct {
		Profiles []struct {
			Name    string          `json:"name"`
			Plugins json.RawMessage `json:"plugins"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(trimmed, &doc); err != nil {
		return nil, fmt.Errorf("plugins.json: %w", err)
	}
	for _, item := range doc.Profiles {
		if item.Name != profile {
			continue
		}
		if len(bytes.TrimSpace(item.Plugins)) == 0 || string(bytes.TrimSpace(item.Plugins)) == "null" {
			return nil, nil
		}
		return parsePlugins(item.Plugins)
	}
	return nil, nil
}

func parsePlugins(data []byte) ([]pluginSource, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var sources []pluginSource
	if err := dec.Decode(&sources); err != nil {
		return nil, fmt.Errorf("plugins.json: %w", err)
	}
	if sources == nil {
		return nil, fmt.Errorf("plugins.json must be an array")
	}
	var extra struct{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("plugins.json: extra data")
	}
	seen := map[string]bool{}
	for i := range sources {
		src := &sources[i]
		if err := validatePlugin(src); err != nil {
			return nil, fmt.Errorf("plugins.json[%d]: %w", i, err)
		}
		if seen[src.Name] {
			return nil, fmt.Errorf("plugins.json: duplicate name %q", src.Name)
		}
		seen[src.Name] = true
	}
	return sources, nil
}

func validatePlugin(src *pluginSource) error {
	if !validPluginName(src.Name) {
		return fmt.Errorf("name %q must be a single directory name", src.Name)
	}
	if !validRevision(src.Revision) {
		return fmt.Errorf("revision must be a full 40-character commit SHA")
	}
	if err := validateRepository(src.Repository); err != nil {
		return err
	}
	cleaned, err := cleanSubpath(src.Path)
	if err != nil {
		return err
	}
	src.Path = cleaned
	src.Revision = strings.ToLower(src.Revision)
	return nil
}

func validPluginName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 128 {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case i > 0 && (r == '.' || r == '_' || r == '-'):
		default:
			return false
		}
	}
	return true
}

func validRevision(rev string) bool {
	if len(rev) != 40 {
		return false
	}
	for _, r := range rev {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

func validateRepository(raw string) error {
	if len(raw) == 0 || len(raw) > 512 || strings.ContainsAny(raw, " \t\r\n") {
		return fmt.Errorf("repository must be an https URL without credentials")
	}
	if strings.Contains(raw, "@") || !strings.HasPrefix(raw, "https://") {
		return fmt.Errorf("repository must be an https URL without credentials")
	}
	rest := strings.TrimPrefix(raw, "https://")
	host, repoPath, ok := strings.Cut(rest, "/")
	if !ok || host == "" || repoPath == "" || strings.Contains(repoPath, "?") || strings.Contains(repoPath, "#") || strings.Contains(repoPath, "\\") {
		return fmt.Errorf("repository must be an https URL without credentials")
	}
	hostName := host
	if h, _, found := strings.Cut(host, ":"); found {
		hostName = h
	}
	if strings.EqualFold(hostName, "github.com") {
		trimmed := strings.TrimSuffix(strings.Trim(repoPath, "/"), ".git")
		parts := strings.Split(trimmed, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("github repository must be https://github.com/owner/repo")
		}
	}
	return nil
}

func cleanSubpath(path string) (string, error) {
	if path == "" || path == "." {
		return "", nil
	}
	if strings.HasPrefix(path, "/") || strings.Contains(path, "\\") || strings.Contains(path, "..") {
		return "", fmt.Errorf("path must be a relative subdirectory without ..")
	}
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("path must be a relative subdirectory without ..")
		}
	}
	return strings.Join(parts, "/"), nil
}

// pointPlugins makes $HERMES_HOME/plugins a symlink to the projection.
// unlink cannot remove a directory. A non-empty real directory is user data
// and is left untouched.
func pointPlugins(home, pluginsDir string) error {
	link := filepath.Join(home, "plugins")
	info, err := os.Lstat(link)
	if os.IsNotExist(err) {
		return os.Symlink(pluginsDir, link)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		current, err := os.Readlink(link)
		if err != nil {
			return err
		}
		if current == pluginsDir {
			return nil
		}
		if err := os.Remove(link); err != nil {
			return err
		}
		return os.Symlink(pluginsDir, link)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s exists and is not a symlink; refusing to replace it", link)
	}
	entries, err := os.ReadDir(link)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s is a non-empty directory; refusing to delete existing plugin data", link)
	}
	if err := os.Remove(link); err != nil {
		return fmt.Errorf("%s is a directory and could not be removed: %w", link, err)
	}
	return os.Symlink(pluginsDir, link)
}
