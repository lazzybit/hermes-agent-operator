package v1alpha1

import (
	"fmt"
	"k8s.io/apimachinery/pkg/util/validation"
	"time"
)

// Settings applies the same defaults outside admission and validates before signing.
func (s SSHSpec) Settings() (string, time.Duration, time.Duration, error) {
	domain := s.ClusterDomain
	if domain == "" {
		domain = "cluster.local"
	}
	lifetime, renew := s.LifetimeSeconds, s.RenewBeforeSeconds
	if lifetime == 0 {
		lifetime = 86400
	}
	if renew == 0 {
		renew = 21600
	}
	if len(validation.IsDNS1123Subdomain(domain)) != 0 || lifetime < 1200 || lifetime > 31536000 || renew < 600 || renew >= lifetime {
		return "", 0, 0, fmt.Errorf("invalid SSH cluster domain or certificate policy")
	}
	return domain, time.Duration(lifetime) * time.Second, time.Duration(renew) * time.Second, nil
}
