package main

import (
	"flag"
	"fmt"
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	"github.com/lazzybit/hermes-agent-operator/internal/controller"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"os"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// namespaceOptions disables cached Secret reads; signing CAS always reads the API.
func namespaceOptions(namespace string) ctrl.Options {
	return ctrl.Options{
		Cache:          cache.Options{DefaultNamespaces: map[string]cache.Config{namespace: {}}, ReaderFailOnMissingInformer: true},
		Client:         client.Options{Cache: &client.CacheOptions{DisableFor: []client.Object{&corev1.Secret{}, &corev1.PersistentVolumeClaim{}, &corev1.ServiceAccount{}}}},
		LeaderElection: true, LeaderElectionNamespace: namespace, LeaderElectionID: "hermes-agent-controller",
		Metrics: metricsserver.Options{BindAddress: "0"}, HealthProbeBindAddress: ":8081",
	}
}

func main() {
	var namespace string
	flag.StringVar(&namespace, "namespace", os.Getenv("WATCH_NAMESPACE"), "Required namespace for all watches and leader election")
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	if namespace == "" {
		fmt.Fprintln(os.Stderr, "--namespace or WATCH_NAMESPACE must be set; cluster-wide operation is disabled")
		os.Exit(1)
	}
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{api.AddToScheme, corev1.AddToScheme, appsv1.AddToScheme, networkingv1.AddToScheme} {
		if err := add(scheme); err != nil {
			panic(err)
		}
	}
	managerOptions := namespaceOptions(namespace)
	managerOptions.Scheme = scheme
	manager, err := ctrl.NewManager(ctrl.GetConfigOrDie(), managerOptions)
	if err != nil {
		panic(err)
	}
	if err = (&controller.ChannelClaimReconciler{Client: manager.GetClient()}).SetupWithManager(manager); err != nil {
		panic(err)
	}
	if err = (&controller.HermesReconciler{Client: manager.GetClient(), Scheme: scheme}).SetupWithManager(manager); err != nil {
		panic(err)
	}
	if err = (&controller.ProfileReconciler{Client: manager.GetClient(), SecretClient: manager.GetClient(), Scheme: scheme}).SetupWithManager(manager); err != nil {
		panic(err)
	}
	if err = manager.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		panic(err)
	}
	if err = manager.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		panic(err)
	}
	if err = manager.Start(ctrl.SetupSignalHandler()); err != nil {
		panic(err)
	}
}
