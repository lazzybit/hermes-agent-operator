package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"os"
	"sigs.k8s.io/yaml"
	"testing"
)

func TestProfileInitializationRequiredSchema(t *testing.T) {
	for _, kind := range []string{"hermes", "hermesprofiles"} {
		data, err := os.ReadFile("../../../config/crd/bases/hermes.nousresearch.com_" + kind + ".yaml")
		if err != nil {
			t.Fatal(err)
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := yaml.UnmarshalStrict(data, &crd); err != nil {
			t.Fatal(err)
		}
		spec := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
		fields := []string{"config", "configMap", "credentialsSecret"}
		config := spec.Properties["config"]
		if config.Type != "object" || config.XPreserveUnknownFields == nil || !*config.XPreserveUnknownFields {
			t.Fatal("config must preserve arbitrary nested Hermes object fields")
		}
		if _, ok := spec.Properties["configSecret"]; !ok {
			t.Fatal("stable configSecret schema must remain")
		}
		if kind == "hermes" {
			if _, ok := spec.Properties["helperImage"]; !ok {
				t.Fatal("stable helperImage schema must remain")
			}
		}
		for _, field := range fields {
			if _, ok := spec.Properties[field]; !ok {
				t.Fatalf("%s.%s missing", kind, field)
			}
		}
		for _, required := range spec.Required {
			if required == "helperImage" || required == "configSecret" {
				t.Fatal("deprecated fields must be optional")
			}
		}
	}
}
