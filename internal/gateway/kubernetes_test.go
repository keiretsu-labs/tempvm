package gateway

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
)

func TestSandboxUsesKataAndGuestImage(t *testing.T) {
	cfg := Config{
		Namespace:     "tempvm",
		GuestImage:    "ghcr.io/keiretsu-labs/tempvm-linux@sha256:test",
		RuntimeClass:  "kata-clh",
		HostKeySecret: "host-key",
		BootTimeout:   time.Minute,
	}
	backend := NewKubernetesBackend(
		cfg,
		kubernetesfake.NewSimpleClientset(),
		fakeDynamic(),
	)
	obj := backend.sandbox("tempvm-test", map[string]string{
		managedLabel: managedValue,
		sessionLabel: "tempvm-test",
	})

	runtimeClass, found, err := unstructuredString(obj.Object, "spec", "podTemplate", "spec", "runtimeClassName")
	if err != nil || !found {
		t.Fatalf("runtimeClassName missing: found=%v err=%v", found, err)
	}
	if runtimeClass != "kata-clh" {
		t.Fatalf("runtimeClassName = %q, want kata-clh", runtimeClass)
	}

	containers, found, err := unstructuredSlice(obj.Object, "spec", "podTemplate", "spec", "containers")
	if err != nil || !found || len(containers) != 1 {
		t.Fatalf("containers malformed: found=%v len=%d err=%v", found, len(containers), err)
	}
	container := containers[0].(map[string]any)
	if got := container["image"]; got != cfg.GuestImage {
		t.Fatalf("guest image = %v, want %q", got, cfg.GuestImage)
	}
}

func TestNewSSHKey(t *testing.T) {
	signer, privateKey, authorizedKey, err := newSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(privateKey) == 0 || len(authorizedKey) == 0 {
		t.Fatal("generated key material is empty")
	}
	if signer.PublicKey().Type() != "ssh-ed25519" {
		t.Fatalf("key type = %q, want ssh-ed25519", signer.PublicKey().Type())
	}
}

func fakeDynamic() *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
}
