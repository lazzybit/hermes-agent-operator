package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func profileRoot() string {
	if v := os.Getenv("HERMES_INIT_PROFILE_ROOT"); v != "" {
		return v
	}
	return "/profile"
}

func instanceRoot() string {
	if v := os.Getenv("HERMES_INIT_INSTANCE_ROOT"); v != "" {
		return v
	}
	return "/instance"
}

func profileLinkTarget(name string) string {
	base := os.Getenv("HERMES_INIT_PROFILE_LINK_ROOT")
	if base == "" {
		base = "/run/hermes-profiles"
	}
	return base + "/" + name + "/.hermes-home"
}

func hermesBin() string {
	if v := os.Getenv("HERMES_BIN"); v != "" {
		return v
	}
	return "/opt/hermes/.venv/bin/hermes"
}

// initializeHome preserves the previous controller semantics. Legacy claims
// that already hold runtime state stay at the mount root. New homes are the
// managed profile created on a temporary sibling, then published as home/.
// config.yaml and .env become symlinks to the read-only projections. SOUL.md
// and other mutable state are left in place.
func initializeHome(profile, configDir, credentials string) (string, error) {
	root := profileRoot()
	legacy := false
	for _, name := range []string{".env", "config.yaml", "SOUL.md", "state.db", "profiles"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			legacy = true
			break
		}
	}
	home := root
	if !legacy {
		home = filepath.Join(root, "home")
	}
	if _, err := os.Stat(home); os.IsNotExist(err) {
		if err := createManagedHome(root, home); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	if err := os.Chmod(home, 0o700); err != nil {
		return "", err
	}
	if err := linkFile(filepath.Join(home, "config.yaml"), filepath.Join(configDir, "config.yaml")); err != nil {
		return "", err
	}
	if credentials != "" {
		if err := linkFile(filepath.Join(home, ".env"), credentials); err != nil {
			return "", err
		}
	}
	if err := ensureHermesHomeLink(root, legacy); err != nil {
		return "", err
	}
	if err := linkInstanceProfile(profile); err != nil {
		return "", err
	}
	return home, nil
}

func createManagedHome(root, home string) error {
	staging, err := os.MkdirTemp(root, ".home-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	cmd := exec.Command(hermesBin(), "profile", "create", "managed", "--no-alias")
	cmd.Env = append(os.Environ(), "HERMES_HOME="+staging)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("hermes profile create: %w", err)
	}
	src := filepath.Join(staging, "profiles", "managed")
	if err := os.Rename(src, home); err != nil {
		return fmt.Errorf("publish managed home: %w", err)
	}
	return nil
}

func ensureHermesHomeLink(root string, legacy bool) error {
	link := filepath.Join(root, ".hermes-home")
	info, err := os.Lstat(link)
	if os.IsNotExist(err) {
		target := "home"
		if legacy {
			target = "."
		}
		return os.Symlink(target, link)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	return fmt.Errorf("%s exists and is not a symlink", link)
}

func linkInstanceProfile(profile string) error {
	if profile == "" {
		return nil
	}
	profiles := filepath.Join(instanceRoot(), ".hermes-home", "profiles")
	if err := os.MkdirAll(profiles, 0o755); err != nil {
		return err
	}
	path := filepath.Join(profiles, profile)
	target := profileLinkTarget(profile)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return os.Symlink(target, path)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(path); err != nil {
			return err
		}
	} else if info.IsDir() {
		// A previous nested mount can leave an empty directory. Never discard
		// state that was left in the instance claim.
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("%s is not an empty directory; refusing to discard it: %w", path, err)
		}
	} else {
		return fmt.Errorf("%s exists and is not a symlink or directory", path)
	}
	return os.Symlink(target, path)
}

func linkFile(path, target string) error {
	if target == "" {
		return nil
	}
	info, err := os.Lstat(path)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		current, err := os.Readlink(path)
		if err != nil {
			return err
		}
		if current == target {
			return nil
		}
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if info.IsDir() {
			return fmt.Errorf("%s is a directory", path)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return os.Symlink(target, path)
}
