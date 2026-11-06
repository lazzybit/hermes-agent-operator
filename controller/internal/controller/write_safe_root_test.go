package controller

import (
	"strings"
	"testing"
)

func TestGatewayWriteSafeRootIncludesManagedPaths(t *testing.T) {
	_, _, h, p := fixture(t)
	d := Gateway(h, nil)
	var roots string
	for _, e := range d.Spec.Template.Spec.Containers[0].Env {
		if e.Name == "HERMES_WRITE_SAFE_ROOT" {
			roots = e.Value
		}
	}
	allowed := map[string]bool{}
	for _, root := range strings.Split(roots, ":") {
		allowed[root] = true
	}
	for _, path := range []string{"/workspace", "/opt/data", "/var/lib/hermes", "/run/hermes-profiles"} {
		if !allowed[path] {
			t.Errorf("managed path %s missing from write safe roots", path)
		}
	}
	if allowed["/"] || allowed[""] {
		t.Fatal("write roots must remain scoped")
	}
	terminal, _ := Terminal(p)
	for _, e := range terminal.Spec.Template.Spec.Containers[0].Env {
		if e.Name == "HERMES_WRITE_SAFE_ROOT" {
			t.Fatal("file guard belongs to gateway, not terminal")
		}
	}
}
