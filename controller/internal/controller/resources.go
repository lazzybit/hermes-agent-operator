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

// initializeHome uses upstream's atomic profile creation on a child of the mounted
// claim. Existing legacy homes stay in place; only declarative file links change.
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
	p.InitContainers = append(p.InitContainers, corev1.Container{Name: "init-" + volume, Image: image, SecurityContext: restrictedContainer(), Command: []string{"/opt/hermes/.venv/bin/python", "-c", nativeInitialize}, Args: []string{home, projection, credentialPath}, Env: []corev1.EnvVar{{Name: "HOME", Value: "/opt/data"}}, ImagePullPolicy: corev1.PullIfNotPresent, TerminationMessagePath: "/dev/termination-log", TerminationMessagePolicy: corev1.TerminationMessageReadFile, VolumeMounts: mounts})
}

const nativeInitialize = `
import os, pathlib, subprocess, sys, tempfile
root = pathlib.Path('/profile')
# Old claims already contain runtime state at their root. Keep that layout.
legacy = any((root / name).exists() for name in ('.env', 'config.yaml', 'SOUL.md', 'state.db', 'profiles'))
home = root if legacy else root / 'home'
if not home.exists():
    with tempfile.TemporaryDirectory(dir=root) as staging:
        subprocess.run(['/opt/hermes/.venv/bin/hermes', 'profile', 'create', 'managed', '--no-alias'], env={**os.environ, 'HERMES_HOME': staging}, check=True)
        (pathlib.Path(staging) / 'profiles' / 'managed').rename(home)
os.chmod(home, 0o700)
for filename, target in [('config.yaml', sys.argv[2] + '/config.yaml'), ('.env', sys.argv[3])]:
    if target:
        path = home / filename
        if not path.is_symlink() or os.readlink(path) != target:
            path.unlink(missing_ok=True)
            path.symlink_to(target)
# Expose a stable home without pre-creating native create's destination.
link = root / '.hermes-home'
if not link.is_symlink():
    link.symlink_to('.' if legacy else 'home', target_is_directory=True)
if sys.argv[1]:
    profiles = pathlib.Path('/instance/.hermes-home/profiles')
    profiles.mkdir(exist_ok=True)
    path = profiles / sys.argv[1]
    target = '/run/hermes-profiles/' + sys.argv[1] + '/.hermes-home'
    if path.is_symlink():
        path.unlink()
    elif path.is_dir():
        # A previous nested PVC mount may leave an empty directory in the parent.
        # Never discard state accidentally left in the instance claim.
        path.rmdir()
    path.symlink_to(target, target_is_directory=True)
`

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
	{
		mode := int32(0444)
		pod.Volumes = append(pod.Volumes, corev1.Volume{Name: "ssh-client", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "gateway-config"}, DefaultMode: &mode, Items: []corev1.KeyToPath{{Key: "ssh_config", Path: "config"}}}}})
		pod.Containers[0].VolumeMounts = append(pod.Containers[0].VolumeMounts, corev1.VolumeMount{Name: "ssh-client", MountPath: "/opt/data/.ssh", ReadOnly: true})
	}
	return d
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
