package v1alpha1

import "testing"

func TestSSHDefaultsAndValidation(t *testing.T) {
	s := SSHSpec{CASecretRef: SSHCASecretReference{Name: "ssh-ca"}}
	domain, lifetime, renew, err := s.Settings()
	if err != nil || domain != "cluster.local" || lifetime.Seconds() != 86400 || renew.Seconds() != 21600 {
		t.Fatalf("defaults: %s %v %v %v", domain, lifetime, renew, err)
	}
	for _, bad := range []SSHSpec{
		{}, {CASecretRef: s.CASecretRef, ClusterDomain: "bad/domain"},
		{CASecretRef: s.CASecretRef, LifetimeSeconds: -1},
		{CASecretRef: s.CASecretRef, LifetimeSeconds: 3600, RenewBeforeSeconds: 3600},
		{CASecretRef: SSHCASecretReference{Name: "bad/name"}},
		{CASecretRef: SSHCASecretReference{Name: "ca", Key: "bad/key"}},
	} {
		if _, _, _, err := bad.Settings(); err == nil {
			t.Fatalf("accepted %#v", bad)
		}
	}
}
