package controller

import (
	"fmt"
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sort"
	"strings"
)

func labels(name, component string) map[string]string {
	return map[string]string{"app.kubernetes.io/managed-by": "hermes-agent-controller", "hermes.nousresearch.com/resource": name, "app.kubernetes.io/component": component}
}
func deployment(name, ns, component, image string, command []string) *appsv1.Deployment {
	one := int32(1)
	no := false
	ls := labels(name, component)
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: ls}, Spec: appsv1.DeploymentSpec{Replicas: &one, Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}, Selector: &metav1.LabelSelector{MatchLabels: ls}, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: ls}, Spec: corev1.PodSpec{AutomountServiceAccountToken: &no, SecurityContext: nonrootPod(), Containers: []corev1.Container{{Name: component, Image: image, Command: command, Args: []string{}, SecurityContext: restrictedContainer()}}}}}}
}
func mount(d *appsv1.Deployment, name, claim, path string, storage api.StorageSpec) {
	p := &d.Spec.Template.Spec
	p.Containers[0].ImagePullPolicy = corev1.PullIfNotPresent
	p.Containers[0].TerminationMessagePath = "/dev/termination-log"
	p.Containers[0].TerminationMessagePolicy = corev1.TerminationMessageReadFile
	source := corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claim}}
	if storage.Ephemeral {
		source = corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}
	}
	p.Volumes = append(p.Volumes, corev1.Volume{Name: name, VolumeSource: source})
	m := corev1.VolumeMount{Name: name, MountPath: path}
	if storage.Ephemeral && name != "workspace" {
		m.SubPath = "home"
	}
	p.Containers[0].VolumeMounts = append(p.Containers[0].VolumeMounts, m)
}

// gatewayInitEntrypoint is the stable init program in the project gateway image.
// A custom Hermes.spec.image must provide the same entry and behavior.
const gatewayInitEntrypoint = "/usr/local/bin/hermes-gateway-init"

func pluginPaths(profile string) (plugins, deps string) {
	id := profile
	if id == "" {
		id = "default"
	}
	return "/run/hermes-plugins/" + id, "/run/hermes-plugin-deps/" + id
}

// initializeHome mounts the projected inputs and invokes the gateway image init.
// Home layout, config and credential links, and plugins.json handling live in
// that entry. Existing legacy homes stay in place; only declarative file links change.
func initializeHome(d *appsv1.Deployment, image, volume, name string, credentials *corev1.SecretKeySelector, home string) {
	p := &d.Spec.Template.Spec
	mode := int32(0440)
	config := "config-" + volume
	p.Volumes = append(p.Volumes, corev1.Volume{Name: config, VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: name}}}})
	projection := "/run/hermes-config/" + volume
	mounts := []corev1.VolumeMount{{Name: volume, MountPath: "/profile"}, {Name: config, MountPath: projection, ReadOnly: true}}
	p.Containers[0].VolumeMounts = append(p.Containers[0].VolumeMounts, mounts[1])
	credentialPath := ""
	if credentials != nil {
		secret := "credentials-" + volume
		credentialPath = "/run/hermes-credentials/" + volume + "/.env"
		p.Volumes = append(p.Volumes, corev1.Volume{Name: secret, VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: credentials.Name, DefaultMode: &mode, Optional: credentials.Optional, Items: []corev1.KeyToPath{{Key: credentials.Key, Path: ".env"}}}}})
		m := corev1.VolumeMount{Name: secret, MountPath: "/run/hermes-credentials/" + volume, ReadOnly: true}
		mounts = append(mounts, m)
		p.Containers[0].VolumeMounts = append(p.Containers[0].VolumeMounts, m)
	}
	plugins, deps := pluginPaths(home)
	pluginVol, depVol := "plugins-"+volume, "plugin-deps-"+volume
	p.Volumes = append(p.Volumes,
		corev1.Volume{Name: pluginVol, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		corev1.Volume{Name: depVol, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
	)
	pluginMount := corev1.VolumeMount{Name: pluginVol, MountPath: plugins}
	depMount := corev1.VolumeMount{Name: depVol, MountPath: deps}
	mounts = append(mounts, pluginMount, depMount)
	roPlugin, roDeps := pluginMount, depMount
	roPlugin.ReadOnly, roDeps.ReadOnly = true, false
	p.Containers[0].VolumeMounts = append(p.Containers[0].VolumeMounts, roPlugin, roDeps)
	p.InitContainers = append(p.InitContainers, corev1.Container{
		Name: "init-" + volume, Image: image, SecurityContext: restrictedContainer(),
		Command: []string{gatewayInitEntrypoint},
		Args: []string{
			"--profile", home,
			"--config", projection,
			"--credentials", credentialPath,
			"--plugins", projection + "/plugins.json",
			"--plugins-dir", plugins,
			"--plugin-deps", deps,
		},
		Env: []corev1.EnvVar{{Name: "HOME", Value: "/opt/data"}}, ImagePullPolicy: corev1.PullIfNotPresent,
		TerminationMessagePath: "/dev/termination-log", TerminationMessagePolicy: corev1.TerminationMessageReadFile, VolumeMounts: mounts,
	})
}

// Gateway sorts a copy so informer objects are never mutated.
func Gateway(h *api.Hermes, profiles []api.HermesProfile) *appsv1.Deployment {
	d := deployment("gateway", h.Namespace, "gateway", h.Spec.Image, h.Spec.Command)
	d.Spec.Template.Labels = labels(d.Name, "gateway")
	d.Spec.Template.Labels["hermes.nousresearch.com/instance"] = h.Name
	pod := &d.Spec.Template.Spec
	uid := int64(10000)
	pod.SecurityContext.RunAsUser, pod.SecurityContext.RunAsGroup, pod.SecurityContext.FSGroup = &uid, &uid, &uid
	// File tools validate paths in the gateway before operating on the SSH backend.
	// Preserve local home/state access while allowing the managed remote workspace.
	pod.Containers[0].Env = []corev1.EnvVar{
		{Name: "HERMES_HOME", Value: "/var/lib/hermes/.hermes-home"},
		{Name: "HOME", Value: "/opt/data"},
		{Name: "HERMES_WRITE_SAFE_ROOT", Value: "/opt/data:/var/lib/hermes:/run/hermes-profiles:/workspace"},
	}
	mount(d, "home", "gateway-data", "/var/lib/hermes", h.Spec.Storage)
	pod.Containers[0].VolumeMounts[0].SubPath = ""
	initializeHome(d, h.Spec.Image, "home", "gateway-config", h.Spec.CredentialsSecret, "")
	ps := append([]api.HermesProfile(nil), profiles...)
	sort.Slice(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
	for _, p := range ps {
		mount(d, "profile-"+p.Name, "profile-data-"+p.Name, "/run/hermes-profiles/"+p.Name, p.Spec.Storage)
		pod.Containers[0].VolumeMounts[len(pod.Containers[0].VolumeMounts)-1].SubPath = ""
		initializeHome(d, h.Spec.Image, "profile-"+p.Name, "profile-config-"+p.Name, p.Spec.CredentialsSecret, p.Name)
		init := &pod.InitContainers[len(pod.InitContainers)-1]
		init.VolumeMounts = append(init.VolumeMounts, corev1.VolumeMount{Name: "home", MountPath: "/instance"})
		sshProjection(d, p.Name, false)
	}
	pod.Containers[0].Env = append(pod.Containers[0].Env, corev1.EnvVar{Name: "PYTHONPATH", Value: pluginPythonPath(ps)})
	{
		mode := int32(0444)
		pod.Volumes = append(pod.Volumes, corev1.Volume{Name: "ssh-client", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "gateway-config"}, DefaultMode: &mode, Items: []corev1.KeyToPath{{Key: "ssh_config", Path: "config"}}}}})
		pod.Containers[0].VolumeMounts = append(pod.Containers[0].VolumeMounts, corev1.VolumeMount{Name: "ssh-client", MountPath: "/opt/data/.ssh", ReadOnly: true})
	}
	return d
}

func pluginPythonPath(profiles []api.HermesProfile) string {
	parts := []string{"/run/hermes-plugin-deps/default"}
	for _, p := range profiles {
		parts = append(parts, "/run/hermes-plugin-deps/"+p.Name)
	}
	return strings.Join(parts, ":")
}

func Terminal(p *api.HermesProfile) (*appsv1.Deployment, *corev1.Service) {
	name := "terminal-" + p.Name
	d := deployment(name, p.Namespace, "terminal", p.Spec.Terminal.Image, managedSSHDCommand())
	d.Spec.Template.Labels = labels(name, "terminal")
	d.Spec.Template.Labels["hermes.nousresearch.com/instance"] = p.Spec.HermesRef
	mount(d, "workspace", "terminal-data-"+p.Name, "/workspace", p.Spec.Terminal.Storage)
	d.Spec.Template.Spec.Containers[0].WorkingDir = "/workspace"
	for _, entry := range p.Spec.Env {
		d.Spec.Template.Spec.Containers[0].Env = append(d.Spec.Template.Spec.Containers[0].Env, *entry.DeepCopy())
	}
	d.Spec.Template.Spec.Containers[0].Ports = []corev1.ContainerPort{{Name: "ssh", ContainerPort: 2222, Protocol: corev1.ProtocolTCP}}
	s := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: p.Namespace, Labels: labels(name, "terminal")}, Spec: corev1.ServiceSpec{Selector: labels(name, "terminal"), Ports: []corev1.ServicePort{{Name: "ssh", Port: 22, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt32(2222)}}}}
	return d, s
}

// Claim intentionally has no owner reference: all persistent data is retained.
func Claim(name, ns string, storage api.StorageSpec) (*corev1.PersistentVolumeClaim, error) {
	q, err := resource.ParseQuantity(storage.Size)
	if err != nil || q.Sign() <= 0 {
		return nil, fmt.Errorf("invalid storage size %q", storage.Size)
	}
	return &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels(name, "storage")}, Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}, StorageClassName: storage.StorageClassName, Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: q}}}}, nil
}
