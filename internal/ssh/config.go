package sshidentity

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Configuration contains OpenSSH file contents. Paths are fixed, injection-safe,
// and match the mounting contract below. These are not Hermes backend settings.
type Configuration struct{ SSHD, Client, KnownHosts, AuthorizedPrincipals, CAPublicKey string }

// Configuration generates a standalone sshd configuration (not an Include
// snippet) for OS account agent. Install its files at /run/hermes-ssh in the
// terminal; install client files at /run/hermes-ssh/<profile> in the gateway.
// Invoke the client with ssh -F <config>; command-line options can override this
// configuration. The per-profile service alias is consumed by the native Hermes SSH backend.
func (a *Authority) Configuration(id Identity) (Configuration, error) {
	if err := id.validate(); err != nil {
		return Configuration{}, err
	}
	ca := string(ssh.MarshalAuthorizedKey(a.signer.PublicKey()))
	root := "/run/hermes-ssh/" + id.Profile
	return Configuration{
		CAPublicKey:          ca,
		AuthorizedPrincipals: id.Principal() + "\n",
		KnownHosts:           "@cert-authority " + id.Hostname() + " " + strings.TrimSpace(ca) + "\n",
		SSHD: fmt.Sprintf(`Port 2222
PidFile /tmp/hermes-sshd.pid
HostKey /run/hermes-ssh-private/%s/key
HostCertificate /run/hermes-ssh/host_key-cert.pub
TrustedUserCAKeys /run/hermes-ssh/ca.pub
AuthorizedPrincipalsFile /run/hermes-authorized-principals
AuthorizedKeysFile none
AuthorizedKeysCommand none
AuthorizedPrincipalsCommand none
PubkeyAuthentication yes
AuthenticationMethods publickey
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitEmptyPasswords no
PermitRootLogin no
UsePAM no
AcceptEnv *
HostbasedAuthentication no
GSSAPIAuthentication no
AllowUsers agent
StrictModes yes
`, id.Profile),
		Client: fmt.Sprintf(`Host terminal-%s
    HostName %s
    User agent
    IdentityFile /run/hermes-ssh-private/%s/key
    CertificateFile %s/id_ed25519-cert.pub
    UserKnownHostsFile %s/known_hosts
    StrictHostKeyChecking yes
    HostKeyAlgorithms ssh-ed25519-cert-v01@openssh.com
`, id.Profile, id.Hostname(), id.Profile, root, root),
	}, nil
}
