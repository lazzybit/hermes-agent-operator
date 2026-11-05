package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"os"
	"sigs.k8s.io/yaml"
	"testing"
)

func TestReservedDefaultProfileAdmission(t *testing.T) {
	data, err := os.ReadFile("../../../config/crd/bases/hermes.nousresearch.com_hermesprofiles.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.UnmarshalStrict(data, &crd); err != nil {
		t.Fatal(err)
	}
	for _, rule := range crd.Spec.Versions[0].Schema.OpenAPIV3Schema.XValidations {
		if rule.Rule == "self.metadata.name != 'default'" && rule.Message != "" {
			return
		}
	}
	t.Fatal("admission accepts reserved default profile")
}

func TestSSHPropagationWindow(t *testing.T) {
	for _, policy := range []struct {
		lifetime, renew int64
		valid           bool
	}{
		{1199, 600, false}, {1200, 599, false}, {1200, 600, true}, {86400, 1, false}, {1200, 1200, false}, {86400, 21600, true},
	} {
		spec := SSHSpec{CASecretRef: SSHCASecretReference{Name: "ca"}, LifetimeSeconds: policy.lifetime, RenewBeforeSeconds: policy.renew}
		_, _, _, err := spec.Settings()
		if (err == nil) != policy.valid {
			t.Errorf("lifetime=%d renew=%d: %v", policy.lifetime, policy.renew, err)
		}
	}
	data, err := os.ReadFile("../../../config/crd/bases/hermes.nousresearch.com_hermes.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.UnmarshalStrict(data, &crd); err != nil {
		t.Fatal(err)
	}
	ssh := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties["ssh"]
	for field, minimum := range map[string]float64{"lifetimeSeconds": 1200, "renewBeforeSeconds": 600} {
		prop := ssh.Properties[field]
		if prop.Minimum == nil || *prop.Minimum != minimum {
			t.Errorf("%s minimum must be %v", field, minimum)
		}
	}
}
