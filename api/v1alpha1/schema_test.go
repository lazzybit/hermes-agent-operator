package v1alpha1

import (
	"os"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// Fake clients bypass admission. Check the generated admission schema directly.
func TestProfileReferenceImmutableAdmissionSchema(t *testing.T) {
	data, err := os.ReadFile("../../config/crd/bases/hermes.nousresearch.com_hermesprofiles.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.UnmarshalStrict(data, &crd); err != nil {
		t.Fatal(err)
	}
	for _, version := range crd.Spec.Versions {
		if version.Name != "v1alpha1" {
			continue
		}
		spec := version.Schema.OpenAPIV3Schema.Properties["spec"]
		ref := spec.Properties["hermesRef"]
		required := false
		for _, name := range spec.Required {
			required = required || name == "hermesRef"
		}
		if !required {
			t.Fatal("hermesRef must be required so removal cannot bypass transition validation")
		}
		for _, rule := range ref.XValidations {
			if strings.ReplaceAll(rule.Rule, " ", "") == "oldSelf==self" && rule.Message != "" {
				return
			}
		}
		t.Fatal("generated admission schema must reject changes to hermesRef with oldSelf == self")
	}
	t.Fatal("v1alpha1 schema missing")
}
