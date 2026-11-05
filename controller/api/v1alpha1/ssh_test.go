package v1alpha1

import "testing"

func TestSSHSettings(t *testing.T) {
	if _, _, _, err := (SSHSpec{}).Settings(); err != nil {
		t.Fatal(err)
	}
	for _, s := range []SSHSpec{{ClusterDomain: "bad/domain"}, {LifetimeSeconds: -1}, {LifetimeSeconds: 3600, RenewBeforeSeconds: 3600}} {
		if _, _, _, err := s.Settings(); err == nil {
			t.Fatal("invalid policy accepted")
		}
	}
}
