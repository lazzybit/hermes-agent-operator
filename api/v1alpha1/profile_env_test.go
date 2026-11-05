package v1alpha1

import (
	"context"
	corev1 "k8s.io/api/core/v1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	celvalidation "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"os"
	"sigs.k8s.io/yaml"
	"slices"
	"testing"
)

func TestProfileEnvironmentSchemaAndDeepCopy(t *testing.T) {
	data, err := os.ReadFile("../../config/crd/bases/hermes.nousresearch.com_hermesprofiles.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err = yaml.UnmarshalStrict(data, &crd); err != nil {
		t.Fatal(err)
	}
	spec := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
	env := spec.Properties["env"]
	if slices.Contains(spec.Required, "env") || env.Type != "array" || env.XListType == nil || *env.XListType != "map" || !slices.Equal(env.XListMapKeys, []string{"name"}) {
		t.Fatal("environment must be optional and keyed by name")
	}
	entry := env.Items.Schema
	if env.MaxItems == nil || *env.MaxItems != 128 || entry.Properties["name"].MaxLength == nil || *entry.Properties["name"].MaxLength != 253 {
		t.Fatal("missing CEL cost bounds")
	}
	if entry.Properties["value"].MaxLength != nil {
		t.Fatal("environment values must not acquire a length limit")
	}
	if !slices.Contains(entry.Required, "name") || len(entry.XValidations) != 3 {
		t.Fatal("missing environment admission rules")
	}
	var internal apiextensions.JSONSchemaProps
	if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(entry, &internal, nil); err != nil {
		t.Fatal(err)
	}
	structural, err := schema.NewStructural(&internal)
	if err != nil {
		t.Fatal(err)
	}
	validator := celvalidation.NewValidator(structural, false, 1000000)
	for _, tc := range []struct {
		value map[string]interface{}
		valid bool
	}{
		{map[string]interface{}{"name": "SERVICE_TOKEN", "value": "literal"}, true},
		{map[string]interface{}{"name": "SERVICE_TOKEN", "valueFrom": map[string]interface{}{"secretKeyRef": map[string]interface{}{"name": "credentials", "key": "token"}}}, true},
		{map[string]interface{}{"name": "BAD=NAME"}, false},
		{map[string]interface{}{"name": "HERMES_TERMINAL_ENV_TOKEN"}, false},
		{map[string]interface{}{"name": "SERVICE_TOKEN", "valueFrom": map[string]interface{}{"fieldRef": map[string]interface{}{"fieldPath": "metadata.name"}}}, false},
		{map[string]interface{}{"name": "SERVICE_TOKEN", "value": "literal", "valueFrom": map[string]interface{}{"secretKeyRef": map[string]interface{}{"name": "credentials", "key": "token"}}}, false},
	} {
		errs, _ := validator.Validate(context.Background(), field.NewPath("env"), structural, tc.value, nil, 1000000)
		if (len(errs) == 0) != tc.valid {
			t.Fatalf("admission valid=%v: %v", tc.valid, errs)
		}
	}
	ref := entry.Properties["valueFrom"].Properties["secretKeyRef"]
	if !slices.Contains(ref.Required, "key") || entry.Properties["value"].Type != "string" {
		t.Fatal("missing native literal or Secret selector schema")
	}
	p := &HermesProfile{Spec: HermesProfileSpec{Env: []corev1.EnvVar{{Name: "SERVICE_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "credentials"}, Key: "token"}}}}}}
	copy := p.DeepCopy()
	copy.Spec.Env[0].ValueFrom.SecretKeyRef.Name = "changed"
	if p.Spec.Env[0].ValueFrom.SecretKeyRef.Name != "credentials" {
		t.Fatal("deep copy aliases nested selector")
	}
	if len((HermesProfileSpec{}).Env) != 0 {
		t.Fatal("zero value injects environment")
	}
}
