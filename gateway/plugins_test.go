package main

import (
	"archive/tar"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginsJSONContract(t *testing.T) {
	sha := strings.Repeat("ab", 20)
	good := []byte(`[
	  {"name":"hindsight","repository":"https://github.com/vectorize-io/hindsight","revision":"` + sha + `","path":"hindsight-integrations/hermes"}
	]`)
	sources, err := parsePlugins(good)
	if err != nil || len(sources) != 1 || sources[0].Path != "hindsight-integrations/hermes" || sources[0].Revision != sha {
		t.Fatalf("parsed %#v %v", sources, err)
	}
	for _, raw := range []string{
		`[{"name":"hindsight","repository":"https://github.com/vectorize-io/hindsight","revision":"main"}]`,
		`[{"name":"hindsight","repository":"https://github.com/vectorize-io/hindsight","revision":"` + sha[:12] + `"}]`,
		`[{"name":"hindsight","repository":"https://github.com/vectorize-io/hindsight","revision":"` + strings.Repeat("g", 40) + `"}]`,
		`[{"name":"hindsight","repository":"https://user:token@github.com/vectorize-io/hindsight","revision":"` + sha + `"}]`,
		`[{"name":"hindsight","repository":"git@github.com:vectorize-io/hindsight.git","revision":"` + sha + `"}]`,
		`[{"name":"../hindsight","repository":"https://github.com/vectorize-io/hindsight","revision":"` + sha + `"}]`,
		`[{"name":"hindsight","repository":"https://github.com/vectorize-io/hindsight","revision":"` + sha + `","path":"../secret"}]`,
		`[{"name":"hindsight","repository":"https://github.com/vectorize-io/hindsight","revision":"` + sha + `","path":"/abs"}]`,
		`[{"name":"hindsight","repository":"https://github.com/vectorize-io/hindsight","revision":"` + sha + `","extra":true}]`,
		`{}`,
		`null`,
		`[{"name":"hindsight","repository":"https://github.com/vectorize-io/hindsight","revision":"` + sha + `"},{"name":"hindsight","repository":"https://example.com/plugin.git","revision":"` + sha + `"}]`,
	} {
		if _, err := parsePlugins([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	empty, err := parsePlugins([]byte("[]"))
	if err != nil || len(empty) != 0 {
		t.Fatal(err)
	}
}

func TestApplyPluginsMissingOrEmptyIsNoop(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "plugins", "local.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyPlugins("", home, filepath.Join(t.TempDir(), "missing.json"), t.TempDir(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(t.TempDir(), "plugins.json")
	if err := os.WriteFile(manifest, []byte("[]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyPlugins("", home, manifest, t.TempDir(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "plugins", "local.txt"))
	if err != nil || string(data) != "keep" {
		t.Fatal("empty plugins.json changed the home")
	}
}

func TestApplyPluginsProjectsTreeAndDependencies(t *testing.T) {
	home := t.TempDir()
	pluginsDir := t.TempDir()
	depsDir := t.TempDir()
	sha := strings.Repeat("cd", 20)
	var fetched []string
	orig := fetchPlugin
	fetchPlugin = func(repo, rev, subpath, dest string) error {
		fetched = append(fetched, repo+"@"+rev+" "+subpath)
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dest, "pyproject.toml"), []byte("[project]\nname = \"hindsight\"\ndependencies = [\n  \"requests>=2.0,<3\",\n]\n"), 0o644)
	}
	t.Cleanup(func() { fetchPlugin = orig })
	installer := filepath.Join(t.TempDir(), "install")
	record := filepath.Join(t.TempDir(), "record")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + record + "\n"
	if err := os.WriteFile(installer, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERMES_PLUGIN_INSTALLER", installer)
	manifest := filepath.Join(t.TempDir(), "plugins.json")
	body := `[{"name":"hindsight","repository":"https://github.com/vectorize-io/hindsight","revision":"` + strings.ToUpper(sha) + `","path":"hindsight-integrations/hermes"}]`
	if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyPlugins("", home, manifest, pluginsDir, depsDir); err != nil {
		t.Fatal(err)
	}
	if len(fetched) != 1 || !strings.Contains(fetched[0], sha) || !strings.Contains(fetched[0], "hindsight-integrations/hermes") {
		t.Fatalf("fetch = %v", fetched)
	}
	link, err := os.Readlink(filepath.Join(home, "plugins"))
	if err != nil || link != pluginsDir {
		t.Fatalf("plugins link = %s %v", link, err)
	}
	if _, err := os.Stat(filepath.Join(home, "plugins", "hindsight", "pyproject.toml")); err != nil {
		t.Fatal(err)
	}
	recorded, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	text := string(recorded)
	if !strings.Contains(text, depsDir) || !strings.Contains(text, "requests>=2.0,<3") {
		t.Fatalf("installer args:\n%s", text)
	}
}

func TestApplyPluginsRefusesNonEmptyDirectory(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "plugins", "mine.py"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	pluginsDir := t.TempDir()
	sha := strings.Repeat("ef", 20)
	orig := fetchPlugin
	fetchPlugin = func(repo, rev, subpath, dest string) error {
		return os.MkdirAll(dest, 0o755)
	}
	t.Cleanup(func() { fetchPlugin = orig })
	manifest := filepath.Join(t.TempDir(), "plugins.json")
	body := `[{"name":"hindsight","repository":"https://example.com/hindsight.git","revision":"` + sha + `"}]`
	if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyPlugins("", home, manifest, pluginsDir, t.TempDir()); err == nil {
		t.Fatal("non-empty plugins directory was replaced")
	}
	data, err := os.ReadFile(filepath.Join(home, "plugins", "mine.py"))
	if err != nil || string(data) != "keep" {
		t.Fatal("user plugin data was removed")
	}
	if _, err := os.Lstat(filepath.Join(home, "plugins")); err != nil {
		t.Fatal(err)
	}
}

func TestApplyPluginsReplacesEmptyDirectory(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	pluginsDir := t.TempDir()
	sha := strings.Repeat("12", 20)
	orig := fetchPlugin
	fetchPlugin = func(repo, rev, subpath, dest string) error {
		return os.MkdirAll(dest, 0o755)
	}
	t.Cleanup(func() { fetchPlugin = orig })
	manifest := filepath.Join(t.TempDir(), "plugins.json")
	body := `[{"name":"tool","repository":"https://example.com/tool.git","revision":"` + sha + `"}]`
	if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyPlugins("", home, manifest, pluginsDir, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(filepath.Join(home, "plugins")); err != nil || target != pluginsDir {
		t.Fatalf("link = %s %v", target, err)
	}
}

func TestSiteCustomizerKeepsEarlierModules(t *testing.T) {
	root := t.TempDir()
	deps := filepath.Join(root, "default")
	if err := writeSiteCustomizer(deps); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deps, "pluginpkg.py"), []byte("value = 'plugin'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	core := t.TempDir()
	if err := os.WriteFile(filepath.Join(core, "pluginpkg.py"), []byte("value = 'core'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", "-c", "import pluginpkg; print(pluginpkg.value)")
	cmd.Env = append(os.Environ(), "PYTHONPATH="+deps+string(os.PathListSeparator)+core)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "core" {
		t.Fatalf("import resolved %q", out)
	}
}

func TestPyprojectDependenciesAndExternalRuntime(t *testing.T) {
	deps, err := projectDependencies([]byte(`
[project]
name = "demo"
dependencies = [
  "alpha>=1,<2",
  'beta[extra]>=3',
]
[project.optional-dependencies]
dev = ["pytest"]
`))
	if err != nil || len(deps) != 2 || deps[0] != "alpha>=1,<2" || deps[1] != "beta[extra]>=3" {
		t.Fatalf("%v %v", deps, err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\ndependencies = [\"requests\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte("name: demo\npython_runtime: external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	called := false
	orig := installRequirements
	installRequirements = func(site string, deps []string) error {
		called = true
		return nil
	}
	t.Cleanup(func() { installRequirements = orig })
	if err := preparePython(dir, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("external runtime installed dependencies")
	}
}

func TestGitHubArchiveExtractsSubpath(t *testing.T) {
	var buf strings.Builder
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	payload := "print('hindsight')\n"
	hdr := &tar.Header{Name: "hindsight-aaaaaaaa/hindsight-integrations/hermes/plugin.py", Mode: 0o644, Size: int64(len(payload))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	evil := &tar.Header{Name: "hindsight-aaaaaaaa/../../outside.txt", Mode: 0o644, Size: 1}
	if err := tw.WriteHeader(evil); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil || gz.Close() != nil {
		t.Fatal(err)
	}
	body := buf.String()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Errorf("missing user agent")
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	origURL := archiveURL
	origClient := archiveClient
	archiveURL = func(owner, repo, rev string) string { return srv.URL + "/" + owner + "/" + repo + "/" + rev }
	archiveClient = srv.Client()
	t.Cleanup(func() {
		archiveURL = origURL
		archiveClient = origClient
	})
	dest := filepath.Join(t.TempDir(), "hindsight")
	err := fetchGitHub("vectorize-io", "hindsight", strings.Repeat("a", 40), "hindsight-integrations/hermes", dest)
	if err == nil {
		t.Fatal("escaping archive entry was accepted")
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(dest), "outside.txt")); !os.IsNotExist(statErr) {
		t.Fatal("archive wrote outside the destination")
	}

	gz = gzip.NewWriter(&buf)
	// Rebuild a clean archive. buf already holds the previous gzip stream.
	buf.Reset()
	gz = gzip.NewWriter(&buf)
	tw = tar.NewWriter(gz)
	hdr = &tar.Header{Name: "hindsight-aaaaaaaa/hindsight-integrations/hermes/plugin.py", Mode: 0o644, Size: int64(len(payload))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil || gz.Close() != nil {
		t.Fatal(err)
	}
	body = buf.String()
	if err := fetchGitHub("vectorize-io", "hindsight", strings.Repeat("a", 40), "hindsight-integrations/hermes", dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "plugin.py"))
	if err != nil || string(got) != payload {
		t.Fatalf("plugin.py = %q %v", got, err)
	}
}

func TestFetchGitSparseSubpath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip(err)
	}
	origin := t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git(origin, "init")
	if err := os.MkdirAll(filepath.Join(origin, "nested", "plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, "nested", "plugin", "plugin.py"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, "README"), []byte("root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(origin, "add", ".")
	git(origin, "commit", "-m", "pin")
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = origin
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	rev := strings.TrimSpace(string(out))
	dest := filepath.Join(t.TempDir(), "plugin")
	if err := fetchGit(origin, rev, "nested/plugin", dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "plugin.py"))
	if err != nil || string(got) != "ok\n" {
		t.Fatalf("plugin.py = %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "README")); !os.IsNotExist(err) {
		t.Fatal("sparse checkout included the repository root")
	}
}
