package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"os"
	"sigs.k8s.io/yaml"
	"testing"
)

func TestSSHAdmissionDefaultsAndBounds(t *testing.T) {
	data, err := os.ReadFile("../../../config/crd/bases/hermes.nousresearch.com_hermes.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.UnmarshalStrict(data, &crd); err != nil {
		t.Fatal(err)
	}
	spec := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
	for _, name := range spec.Required {
		if name == "ssh" {
			t.Fatal("SSH must be opt-in")
		}
	}
	ssh := spec.Properties["ssh"]
	if ssh.Default != nil || len(ssh.Required) != 1 || ssh.Required[0] != "caSecretRef" {
		t.Fatal("CA must be explicit")
	}
	for key, value := range map[string]string{"clusterDomain": "\"cluster.local\"", "lifetimeSeconds": "86400", "renewBeforeSeconds": "21600"} {
		p := ssh.Properties[key]
		if p.Default == nil || string(p.Default.Raw) != value {
			t.Fatalf("default %s", key)
		}
	}
	if len(ssh.XValidations) != 1 || ssh.XValidations[0].Rule != "self.renewBeforeSeconds < self.lifetimeSeconds" {
		t.Fatal("renewal policy not bounded")
	}
	if ssh.Properties["lifetimeSeconds"].Minimum == nil || ssh.Properties["lifetimeSeconds"].Maximum == nil || ssh.Properties["clusterDomain"].Pattern == "" {
		t.Fatal("missing validation")
	}
}
