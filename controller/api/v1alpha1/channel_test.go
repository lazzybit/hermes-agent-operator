package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime"
	"testing"
)

func TestChannelAPIsRegistered(t *testing.T) {
	s := runtime.NewScheme()
	if err := AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"Channel", "ChannelClaim", "ChannelClass"} {
		if _, err := s.New(GroupVersion.WithKind(kind)); err != nil {
			t.Errorf("missing API %s: %v", kind, err)
		}
	}
}
