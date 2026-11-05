//go:build ignore

// Bound the imported EnvVar name property, which controller-gen field markers
// cannot address without replacing the public []corev1.EnvVar Go API.
package main

import (
	"bytes"
	"os"
)

func main() {
	const path = "config/crd/bases/hermes.nousresearch.com_hermesprofiles.yaml"
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	// 253 is an API budget bound, not a shell or Kubernetes EnvVar name limit.
	old := []byte("                      description: Name of the environment variable. Must be a C_IDENTIFIER.\n                      type: string")
	bounded := bytes.Replace(old, []byte("                      type: string"), []byte("                      maxLength: 253\n                      type: string"), 1)
	if bytes.Count(data, old) != 1 {
		panic("expected exactly one EnvVar name schema; inspect controller-gen output")
	}
	if err := os.WriteFile(path, bytes.Replace(data, old, bounded, 1), 0644); err != nil {
		panic(err)
	}
}
