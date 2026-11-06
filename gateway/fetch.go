package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const maxPluginBytes = 256 << 20

var (
	fetchPlugin   = fetchPluginDefault
	archiveClient = &http.Client{
		Timeout:   3 * time.Minute,
		Transport: &http.Transport{DisableCompression: true},
	}
	archiveURL = githubArchiveURL
)

func fetchPluginDefault(repo, rev, subpath, dest string) error {
	if owner, name, ok := githubRepo(repo); ok {
		return fetchGitHub(owner, name, rev, subpath, dest)
	}
	return fetchGit(repo, rev, subpath, dest)
}

func githubRepo(repo string) (owner, name string, ok bool) {
	body := strings.TrimPrefix(repo, "https://")
	if body == repo {
		return "", "", false
	}
	host, rawPath, found := strings.Cut(body, "/")
	if !found {
		return "", "", false
	}
	hostName := host
	if h, _, split := strings.Cut(host, ":"); split {
		hostName = h
	}
	if !strings.EqualFold(hostName, "github.com") {
		return "", "", false
	}
	trimmed := strings.TrimSuffix(strings.Trim(rawPath, "/"), ".git")
	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func githubArchiveURL(owner, repo, rev string) string {
	return "https://codeload.github.com/" + owner + "/" + repo + "/tar.gz/" + rev
}

func fetchGitHub(owner, repo, rev, subpath, dest string) error {
	url := archiveURL(owner, repo, rev)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "hermes-gateway-init")
	resp, err := archiveClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: %s", url, resp.Status)
	}
	gz, err := gzip.NewReader(io.LimitReader(resp.Body, maxPluginBytes+1))
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	defer gz.Close()
	return extractTar(gz, subpath, dest)
}

func extractTar(r io.Reader, subpath, dest string) error {
	tr := tar.NewReader(r)
	wrote := int64(0)
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		rel, ok, err := archiveRelative(hdr.Name, subpath)
		if err != nil {
			return err
		}
		if !ok {
			if _, err := io.Copy(io.Discard, tr); err != nil {
				return err
			}
			continue
		}
		found = true
		target := dest
		if rel != "" {
			target = filepath.Join(dest, filepath.FromSlash(rel))
		}
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if wrote+hdr.Size > maxPluginBytes {
				return fmt.Errorf("plugin archive exceeds %d bytes", maxPluginBytes)
			}
			if err := writeFile(target, tr, hdr.FileInfo().Mode()); err != nil {
				return err
			}
			wrote += hdr.Size
		case tar.TypeSymlink:
			if err := extractSymlink(target, hdr.Linkname); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported archive entry %s", hdr.Name)
		}
	}
	if !found {
		return fmt.Errorf("archive has no files at %q", displayPath(subpath))
	}
	return nil
}

func displayPath(subpath string) string {
	if subpath == "" {
		return "."
	}
	return subpath
}

func archiveRelative(name, subpath string) (string, bool, error) {
	if strings.HasPrefix(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return "", false, fmt.Errorf("archive path escapes plugin directory")
	}
	clean := path.Clean("/" + name)
	if clean == "/" {
		return "", false, nil
	}
	parts := strings.Split(strings.TrimPrefix(clean, "/"), "/")
	if len(parts) < 2 {
		return "", false, nil
	}
	rel := strings.Join(parts[1:], "/")
	if subpath != "" {
		if rel != subpath && !strings.HasPrefix(rel, subpath+"/") {
			return "", false, nil
		}
		rel = strings.TrimPrefix(rel, subpath)
		rel = strings.TrimPrefix(rel, "/")
	}
	if rel == ".." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
		return "", false, fmt.Errorf("archive path escapes plugin directory")
	}
	return rel, true, nil
}

func writeFile(target string, r io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode&0o755)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return err
	}
	return f.Chmod(mode & 0o755)
}

func extractSymlink(target, linkname string) error {
	if filepath.IsAbs(linkname) || strings.Contains(linkname, "..") {
		return fmt.Errorf("archive symlink %s escapes plugin directory", linkname)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.Symlink(linkname, target)
}

func fetchGit(repo, rev, subpath, dest string) error {
	tmp, err := os.MkdirTemp("", "hermes-plugin-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	git := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = tmp
		cmd.Env = append(os.Environ(),
			"GIT_TERMINAL_PROMPT=0",
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_GLOBAL=/dev/null",
		)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git %s: %w", args[0], err)
		}
		return nil
	}
	if err := git("init"); err != nil {
		return err
	}
	if err := git("remote", "add", "origin", repo); err != nil {
		return err
	}
	if subpath != "" {
		if err := git("sparse-checkout", "init", "--cone"); err != nil {
			return err
		}
		if err := git("sparse-checkout", "set", "--cone", subpath); err != nil {
			return err
		}
	}
	if err := git("fetch", "--depth", "1", "origin", rev); err != nil {
		return err
	}
	if err := git("checkout", "--detach", "FETCH_HEAD"); err != nil {
		return err
	}
	src := tmp
	if subpath != "" {
		src = filepath.Join(tmp, filepath.FromSlash(subpath))
	}
	info, err := os.Stat(src)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("revision %s has no directory %q", rev, displayPath(subpath))
	}
	return copyTree(src, dest)
}

func copyTree(src, dest string) error {
	return filepath.WalkDir(src, func(current string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, current)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dest, 0o755)
		}
		if d.Name() == ".git" && rel == ".git" {
			return filepath.SkipDir
		}
		target := filepath.Join(dest, rel)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(current)
			if err != nil {
				return err
			}
			return extractSymlink(target, link)
		}
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file %s", rel)
		}
		in, err := os.Open(current)
		if err != nil {
			return err
		}
		err = writeFile(target, in, info.Mode())
		in.Close()
		return err
	})
}
