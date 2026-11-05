package main

import (
	corev1 "k8s.io/api/core/v1"
	"reflect"
	"testing"
)

func TestSecretsBypassManagerCache(t *testing.T) {
	opts := namespaceOptions("test")
	found := false
	for _, obj := range opts.Client.Cache.DisableFor {
		found = found || reflect.TypeOf(obj) == reflect.TypeOf(&corev1.Secret{})
	}
	if !found {
		t.Fatal("Secrets must bypass informer cache")
	}
	pvcDirect := false
	for _, obj := range opts.Client.Cache.DisableFor {
		pvcDirect = pvcDirect || reflect.TypeOf(obj) == reflect.TypeOf(&corev1.PersistentVolumeClaim{})
	}
	if !pvcDirect {
		t.Fatal("unwatched retained PVC reads must bypass cache when missing informers are rejected")
	}
	if !opts.Cache.ReaderFailOnMissingInformer {
		t.Fatal("prevent accidental Secret informer creation")
	}
}
