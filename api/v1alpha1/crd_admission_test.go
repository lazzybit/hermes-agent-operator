package v1alpha1

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	crdvalidation "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	"sigs.k8s.io/yaml"
)

// Validate the complete generated CRDs at the API server's creation boundary,
// including static per-expression and whole-schema CEL cost limits.
func TestGeneratedCRDAdmission(t *testing.T) {
	paths, err := filepath.Glob("../../config/crd/bases/*.yaml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("CRD paths: %v", err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var external apiextensionsv1.CustomResourceDefinition
			if err := yaml.UnmarshalStrict(data, &external); err != nil {
				t.Fatal(err)
			}
			apiextensionsv1.SetDefaults_CustomResourceDefinition(&external)
			var internal apiextensions.CustomResourceDefinition
			if err := apiextensionsv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(&external, &internal, nil); err != nil {
				t.Fatal(err)
			}
			if errs := crdvalidation.ValidateCustomResourceDefinition(context.Background(), &internal); len(errs) != 0 {
				t.Fatalf("CRD admission rejected: %v", errs.ToAggregate())
			}
		})
	}
}
