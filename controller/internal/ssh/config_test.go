package sshidentity

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh/knownhosts"
)

func TestStrictConfigurationAndHostCA(t *testing.T) {
	a, _ := testAuthority(t)
	id := testIdentity()
	now := time.Now().Truncate(time.Second)
	state, _, err := a.Ensure(id, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	config, err := a.Configuration(id)
	if err != nil {
		t.Fatal(err)
	}
	if config.AuthorizedPrincipals != id.Principal()+"\n" {
		t.Fatal("principal authorization is not fixed")
	}
	for _, directive := range []string{"Port 2222", "PidFile /tmp/hermes-sshd.pid", "AuthorizedPrincipalsFile /run/hermes-authorized-principals", "TrustedUserCAKeys /run/hermes-ssh/ca.pub", "HostKey /run/hermes-ssh-private/coder/key", "HostCertificate /run/hermes-ssh/host_key-cert.pub", "AuthorizedKeysFile none", "PasswordAuthentication no", "KbdInteractiveAuthentication no", "PermitRootLogin no", "AllowUsers agent", "AuthenticationMethods publickey"} {
		if !strings.Contains(config.SSHD, directive+"\n") {
			t.Fatalf("missing sshd directive %q", directive)
		}
	}
	for _, directive := range []string{"Host terminal-coder", "StrictHostKeyChecking yes", "UserKnownHostsFile /run/hermes-ssh/coder/known_hosts", "IdentityFile /run/hermes-ssh-private/coder/key", "CertificateFile /run/hermes-ssh/coder/id_ed25519-cert.pub", "HostKeyAlgorithms ssh-ed25519-cert-v01@openssh.com"} {
		if !strings.Contains(config.Client, directive+"\n") {
			t.Fatalf("missing client directive %q", directive)
		}
	}

	if strings.Contains(config.Client, "accept-new") || strings.Contains(config.KnownHosts, "*") {
		t.Fatal("weakened host checking")
	}
	if !strings.HasPrefix(config.KnownHosts, "@cert-authority "+id.Hostname()+" ") {
		t.Fatal("missing exact host CA binding")
	}
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, []byte(config.KnownHosts), 0600); err != nil {
		t.Fatal(err)
	}
	verify, err := knownhosts.New(path)
	if err != nil {
		t.Fatal(err)
	}
	host := certificate(t, state.HostCertificate)
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 22}
	if err := verify(id.Hostname()+":22", addr, host); err != nil {
		t.Fatalf("host CA verification: %v", err)
	}
	if err := verify("writer-terminal.lab.svc.cluster.local:22", addr, host); err == nil {
		t.Fatal("wrong host trusted")
	}
	if err := verify(id.Hostname()+":22", addr, host.Key); err == nil {
		t.Fatal("raw host key trusted")
	}
	rogue, _ := testAuthority(t)
	rs, _, err := rogue.Ensure(id, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(id.Hostname()+":22", addr, certificate(t, rs.HostCertificate)); err == nil {
		t.Fatal("foreign CA trusted")
	}
	if sshPath, err := exec.LookPath("ssh"); err == nil {
		clientPath := filepath.Join(t.TempDir(), "ssh_config")
		if err := os.WriteFile(clientPath, []byte(config.Client), 0600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(sshPath, "-G", "-F", clientPath, "terminal-coder").CombinedOutput()
		if err != nil {
			t.Fatalf("OpenSSH config parsing: %v: %s", err, out)
		}
		for _, expected := range []string{"stricthostkeychecking true", "user agent"} {
			if !strings.Contains(string(out), expected+"\n") {
				t.Fatalf("OpenSSH effective config missing %q: %s", expected, out)
			}
		}
	} else {
		t.Log("ssh binary absent; effective client config check skipped")
	}
	if _, err := a.Configuration(Identity{}); err == nil {
		t.Fatal("invalid config identity accepted")
	}
}
