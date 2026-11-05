package controller

import (
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"testing"
)

func TestTerminalIsolationAndRetention(t *testing.T) {
	p := &api.HermesProfile{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "test"}, Spec: api.HermesProfileSpec{Terminal: api.TerminalSpec{Image: "terminal:test", Command: []string{"/usr/sbin/sshd", "-D"}, Storage: api.StorageSpec{Size: "2Gi"}}}}
	d, s := Terminal(p)
	pod := d.Spec.Template.Spec
	if *pod.AutomountServiceAccountToken || len(pod.Volumes) != 1 || pod.Volumes[0].PersistentVolumeClaim.ClaimName != "terminal-data-alpha" || pod.Containers[0].VolumeMounts[0].MountPath != "/workspace" {
		t.Fatal("terminal must mount only its workspace without API credentials")
	}
	if s.Spec.Ports[0].Port != 22 || s.Spec.Ports[0].TargetPort.IntVal != 2222 || pod.Containers[0].Ports[0].ContainerPort != 2222 || !reflect.DeepEqual(s.Spec.Selector, d.Spec.Selector.MatchLabels) {
		t.Fatal("service does not select terminal")
	}
	claim, err := Claim("terminal-data-alpha", "test", p.Spec.Terminal.Storage)
	if err != nil || len(claim.OwnerReferences) != 0 {
		t.Fatal("PVC must be retained")
	}
	if _, err := Claim("bad", "test", api.StorageSpec{Size: "-1Gi"}); err == nil {
		t.Fatal("negative size accepted")
	}
}

func TestGatewayDeterministicProfileMounts(t *testing.T) {
	h := &api.Hermes{ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "test"}, Spec: api.HermesSpec{Image: "gateway:test", Command: []string{"hermes", "gateway"}, Storage: api.StorageSpec{Size: "1Gi"}, ConfigSecret: "gateway-config"}}
	a := api.HermesProfile{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "test"}, Spec: api.HermesProfileSpec{HermesRef: "demo"}}
	b := a
	b.Name = "beta"
	d := Gateway(h, []api.HermesProfile{b, a})
	if !reflect.DeepEqual(d, Gateway(h, []api.HermesProfile{a, b})) {
		t.Fatal("profile ordering changes pod template")
	}
	if d.Spec.Replicas == nil || *d.Spec.Replicas != 1 || d.Spec.Strategy.Type != "Recreate" {
		t.Fatal("gateway must have one writer with Recreate updates")
	}
	p := d.Spec.Template.Spec
	if *p.AutomountServiceAccountToken {
		t.Fatal("gateway has API token")
	}
	mounts := p.Containers[0].VolumeMounts
	if len(mounts) != 11 || mounts[2].MountPath != "/run/hermes-profiles/alpha" || mounts[6].MountPath != "/run/hermes-profiles/beta" {
		t.Fatalf("unexpected mounts: %#v", mounts)
	}
	if p.Volumes[1].ConfigMap.Name != "gateway-config" || !p.InitContainers[0].VolumeMounts[1].ReadOnly {
		t.Fatal("config must reference a read-only ConfigMap")
	}
}
