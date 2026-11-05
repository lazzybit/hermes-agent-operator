package sshidentity

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestOpenSSH uses real ssh/sshd processes; only Kubernetes Secret storage is fake.
func TestOpenSSH(t *testing.T) {
	if os.Getenv("HERMES_SSH_INTEGRATION") != "1" {
		t.Skip("set HERMES_SSH_INTEGRATION=1 to launch local OpenSSH")
	}
	if os.Getenv("HERMES_IMAGE_TEST") == "1" {
		if os.Getuid() != 65532 {
			t.Fatal("terminal must run as agent UID 65532")
		}
		for _, dir := range []string{"/workspace", "/home/agent"} {
			f, err := os.CreateTemp(dir, "packaging-check-")
			must(t, err)
			must(t, f.Close())
			must(t, os.Remove(f.Name()))
		}
		if _, err := os.Stat("/usr/sbin/hermes-session-env"); !os.IsNotExist(err) {
			t.Fatal("obsolete environment wrapper installed")
		}
	}
	sshd := sshBinary(t, "SSHD", "sshd")
	sshbin := sshBinary(t, "SSH", "ssh")
	account, err := user.Current()
	must(t, err)
	if strings.ContainsAny(account.Username, " \t\r\n") {
		t.Fatal("unsupported account name")
	}
	root := t.TempDir()
	// OpenSSH config parsing is deliberately kept simple: fail rather than misquote paths.
	if strings.ContainsAny(root, " \t\r\n\"") {
		t.Fatal("TMPDIR must not contain whitespace or quotes")
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	must(t, err)
	block, err := ssh.MarshalPrivateKey(key, "integration-only")
	must(t, err)
	scheme := runtime.NewScheme()
	must(t, corev1.AddToScheme(scheme))
	store := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "integration", Name: "test-ca"},
		Data:       map[string][]byte{"ca_key": pem.EncodeToMemory(block)},
	}).Build()
	signer, err := NewSecretSigner(store, "integration", CASecretReference{Name: "test-ca"}, Policy{Lifetime: time.Hour, RenewBefore: 10 * time.Minute, Backdate: time.Minute})
	must(t, err)
	type identity struct {
		id     Identity
		config Configuration
		secret corev1.Secret
	}
	issue := func(profile string, now time.Time) identity {
		id := Identity{Namespace: "integration", Instance: "test", Profile: profile, ClusterDomain: "cluster.local"}
		result, err := signer.Ensure(context.Background(), id, now)
		must(t, err)
		var secret corev1.Secret
		must(t, store.Get(context.Background(), result.Secret, &secret))
		return identity{id, result.Configuration, secret}
	}
	coding := issue("coding", time.Now().Add(-51*time.Minute))
	research := issue("research", time.Now())
	expired := issue("expired", time.Now().Add(-2*time.Hour))
	write := func(path string, data []byte) {
		must(t, os.MkdirAll(filepath.Dir(path), 0700))
		must(t, os.WriteFile(path, data, 0600))
	}
	for _, item := range []identity{coding, research, expired} {
		dir := filepath.Join(root, item.id.Profile)
		write(filepath.Join(dir, "private", "key"), item.secret.Data[UserPrivateKey])
		write(filepath.Join(dir, "id_ed25519-cert.pub"), item.secret.Data[UserCertificate])
		write(filepath.Join(dir, "known_hosts"), []byte(item.config.KnownHosts))
	}
	clientConfig := func(name string, terminal identity, credential identity) string {
		config := strings.ReplaceAll(terminal.config.Client, "/run/hermes-ssh-private/"+terminal.id.Profile, filepath.Join(root, credential.id.Profile, "private"))
		config = strings.ReplaceAll(config, "/run/hermes-ssh/"+terminal.id.Profile, filepath.Join(root, credential.id.Profile))
		// Keep terminal trust separate from the credential under test.
		config = strings.ReplaceAll(config, "UserKnownHostsFile "+filepath.Join(root, credential.id.Profile, "known_hosts"), "UserKnownHostsFile "+filepath.Join(root, terminal.id.Profile, "known_hosts"))
		config = strings.ReplaceAll(config, "User agent", "User "+account.Username)
		path := filepath.Join(root, name+".client")
		write(path, []byte(config))
		return path
	}
	start := func(name string, terminal identity, host identity) (string, string) {
		dir := filepath.Join(root, name)
		must(t, os.MkdirAll(dir, 0700))
		write(filepath.Join(dir, "private", "key"), host.secret.Data[HostPrivateKey])
		write(filepath.Join(dir, "host_key-cert.pub"), host.secret.Data[HostCertificate])
		write(filepath.Join(dir, "ca.pub"), []byte(terminal.config.CAPublicKey))
		write(filepath.Join(dir, "authorized_principals"), []byte(terminal.config.AuthorizedPrincipals))
		// Kubernetes projected volume roots are writable even when mounted read-only.
		must(t, os.Chmod(dir, 0777))
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		must(t, err)
		port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
		must(t, listener.Close())
		config := strings.ReplaceAll(terminal.config.SSHD, "/run/hermes-ssh-private/"+terminal.id.Profile, filepath.Join(dir, "private"))
		config = strings.ReplaceAll(config, "/run/hermes-ssh", dir)
		// A file subPath mount resolves directly beneath the safe /run parent,
		// bypassing the projected root; a hard link reproduces that inode layout.
		principals := filepath.Join(root, name+"-authorized-principals")
		must(t, os.Link(filepath.Join(dir, "authorized_principals"), principals))
		config = strings.ReplaceAll(config, "/run/hermes-authorized-principals", principals)
		config = strings.ReplaceAll(config, "Port 2222", "Port "+port)
		config = strings.ReplaceAll(config, "AllowUsers agent", "AllowUsers "+account.Username)
		if os.Geteuid() == 0 {
			config = strings.ReplaceAll(config, "PermitRootLogin no", "PermitRootLogin prohibit-password")
		}
		config += "\nListenAddress 127.0.0.1\nPidFile " + filepath.Join(dir, "sshd.pid") + "\nLogLevel VERBOSE\n"
		// OpenSSH 9.8+ splits session handling into a separate executable. Debian's
		// extracted binaries need this override to avoid /usr/lib/openssh.
		if session := os.Getenv("SSHD_SESSION"); session != "" {
			config += "SshdSessionPath " + session + "\n"
		}
		if auth := os.Getenv("SSHD_AUTH"); auth != "" {
			config += "SshdAuthPath " + auth + "\n"
		}
		configPath := filepath.Join(dir, "sshd_config")
		write(configPath, []byte(config))
		check := exec.Command(sshd, "-t", "-f", configPath)
		if out, err := check.CombinedOutput(); err != nil {
			t.Fatalf("sshd preflight: %v\n%s", err, out)
		}
		logPath := filepath.Join(dir, "sshd.log")
		log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0600)
		must(t, err)
		cmd := exec.Command(sshd, "-D", "-e", "-f", configPath)
		cmd.Stdout = log
		cmd.Stderr = log
		must(t, cmd.Start())
		done := make(chan struct{})
		var waitErr error
		go func() { waitErr = cmd.Wait(); close(done) }()
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("sshd did not stop")
			}
			_ = log.Close()
		})
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			conn, err := net.DialTimeout("tcp4", "127.0.0.1:"+port, 100*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				return port, logPath
			}
			select {
			case <-done:
				data, _ := os.ReadFile(logPath)
				t.Fatalf("sshd exited: %v\n%s", waitErr, data)
			default:
			}
			time.Sleep(25 * time.Millisecond)
		}
		data, _ := os.ReadFile(logPath)
		t.Fatalf("sshd never listened\n%s", data)
		return "", ""
	}
	codingPort, codingLog := start("coding-server", coding, coding)
	researchPort, researchLog := start("research-server", research, research)
	wrongPort, wrongLog := start("wrong-host-server", coding, research)
	run := func(t *testing.T, name string, terminal, credential identity, port, logPath, wantError string) {
		t.Helper()
		config := clientConfig(name, terminal, credential)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, sshbin, "-v", "-F", config, "-o", "HostName=127.0.0.1", "-o", "HostKeyAlias="+terminal.id.Hostname(), "-o", "HostKeyAlgorithms=ssh-ed25519-cert-v01@openssh.com", "-o", "ConnectTimeout=3", "-o", "SendEnv=SERVICE_TOKEN", "-p", port, "terminal-"+terminal.id.Profile, `test "$SERVICE_TOKEN" = 'fixture with spaces "quotes" and \backslash' && printf hermes-ssh-ok`)
		cmd.Env = append(os.Environ(), `SERVICE_TOKEN=fixture with spaces "quotes" and \backslash`)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		serverLog, _ := os.ReadFile(logPath)
		if wantError == "" {
			if err != nil || stdout.String() != "hermes-ssh-ok" {
				t.Fatalf("login: %v stdout=%q\nclient:\n%s\nserver:\n%s", err, stdout.String(), stderr.String(), serverLog)
			}
			t.Log("certificate login succeeded and remote command returned hermes-ssh-ok")
		} else {
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 255 || stdout.Len() != 0 {
				t.Fatalf("expected SSH rejection, got %v stdout=%q\n%s", err, stdout.String(), stderr.String())
			}
			evidence := stderr.String() + string(serverLog)
			if !strings.Contains(evidence, wantError) {
				t.Fatalf("rejection missing %q\n%s", wantError, evidence)
			}
			t.Logf("SSH rejected with exit 255; diagnostic: %s", wantError)
		}
	}
	t.Run("matching_coding", func(t *testing.T) { run(t, "match", coding, coding, codingPort, codingLog, "") })
	// A successful research login proves the negative case isn't a broken server.
	t.Run("matching_research_control", func(t *testing.T) { run(t, "research-control", research, research, researchPort, researchLog, "") })
	t.Run("coding_rejected_by_research", func(t *testing.T) {
		run(t, "cross-profile", research, coding, researchPort, researchLog, "Certificate does not contain an authorized principal")
	})
	t.Run("wrong_host_principal", func(t *testing.T) {
		run(t, "wrong-host", coding, coding, wrongPort, wrongLog, "Certificate invalid: name is not a listed principal")
	})
	// Keep the coding principal and CA; issue a fresh credential in the past.
	authority, err := NewAuthority(pem.EncodeToMemory(block), Policy{Lifetime: time.Hour, RenewBefore: 10 * time.Minute, Backdate: time.Minute})
	must(t, err)
	expiredState, _, err := authority.Ensure(coding.id, nil, time.Now().Add(-2*time.Hour))
	must(t, err)
	write(filepath.Join(root, "expired", "private", "key"), expiredState.UserPrivateKey)
	write(filepath.Join(root, "expired", "id_ed25519-cert.pub"), expiredState.UserCertificate)
	t.Run("expired_user_certificate", func(t *testing.T) {
		run(t, "expired", coding, expired, codingPort, codingLog, "Certificate invalid: expired")
	})
}

func sshBinary(t *testing.T, env, fallback string) string {
	t.Helper()
	name := os.Getenv(env)
	if name == "" {
		name = fallback
	}
	path, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("%s unavailable: %v (set %s to an absolute binary path)", fallback, err, env)
	}
	path, err = filepath.Abs(path)
	must(t, err)
	return path
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(fmt.Errorf("integration setup: %w", err))
	}
}
