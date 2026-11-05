package sshidentity

import (
	"context"
	"testing"
)

func TestResultExposesHostCertificateForReload(t *testing.T) {
	c, id, now := fixture(t)
	result, err := adapter(t, c).Ensure(context.Background(), id, now)
	if err != nil {
		t.Fatal(err)
	}
	state := stored(t, c, result.Secret)
	if result.HostCertificate == "" || result.HostCertificate != string(state.Data[HostCertificate]) {
		t.Fatal("reload hash must use persisted public host certificate")
	}
}
