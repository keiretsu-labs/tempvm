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

func TestSandboxHasSessionLabelsAndShutdownTime(t *testing.T) {
	startedAt := time.Date(2026, time.October, 5, 12, 30, 0, 0, time.UTC)
	cfg := DefaultConfig()
	cfg.Namespace = "tempvm"
	cfg.GuestImage = "ghcr.io/keiretsu-labs/tempvm-linux@sha256:test"
	backend := NewKubernetesBackend(
		cfg,
		kubernetesfake.NewSimpleClientset(),
		fakeDynamic(),
	)
	labels := sessionLabels("tempvm-test", startedAt)
	obj := backend.sandboxAt("tempvm-test", labels, startedAt)

	wantLabels := map[string]string{
		managedLabel:   managedValue,
		sessionLabel:   "tempvm-test",
		startedAtLabel: "1791203400",
	}
	for key, want := range wantLabels {
		if got := obj.GetLabels()[key]; got != want {
			t.Errorf("Sandbox label %q = %q, want %q", key, got, want)
		}
		got, found, err := unstructuredString(obj.Object, "spec", "podTemplate", "metadata", "labels", key)
		if err != nil || !found || got != want {
			t.Errorf("pod label %q = %q, found=%t err=%v, want %q", key, got, found, err, want)
		}
	}

	shutdownTime, found, err := unstructuredString(obj.Object, "spec", "shutdownTime")
	if err != nil || !found {
		t.Fatalf("shutdownTime missing: found=%t err=%v", found, err)
	}
	wantShutdown := startedAt.Add(4 * time.Hour).Format(time.RFC3339)
	if shutdownTime != wantShutdown {
		t.Fatalf("shutdownTime = %q, want %q", shutdownTime, wantShutdown)
	}
}

func TestSandboxOmitsShutdownTimeWhenTTLDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Namespace = "tempvm"
	cfg.GuestImage = "ghcr.io/keiretsu-labs/tempvm-linux@sha256:test"
	cfg.MaxSessionTTL = 0
	backend := NewKubernetesBackend(cfg, kubernetesfake.NewSimpleClientset(), fakeDynamic())
	obj := backend.sandboxAt("tempvm-test", sessionLabels("tempvm-test", time.Now()), time.Now())

	if _, found, err := unstructuredString(obj.Object, "spec", "shutdownTime"); err != nil || found {
		t.Fatalf("shutdownTime found=%t err=%v, want omitted", found, err)
	}
}

func TestHasSessionLabelSupportsUpgradeCleanup(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   bool
	}{
		{name: "current", labels: map[string]string{sessionLabel: "tempvm-current"}, want: true},
		{name: "legacy", labels: map[string]string{legacySessionLabel: "tempvm-legacy"}, want: true},
		{name: "host key", labels: map[string]string{managedLabel: managedValue}, want: false},
		{name: "empty", labels: nil, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hasSessionLabel(test.labels); got != test.want {
				t.Fatalf("hasSessionLabel() = %t, want %t", got, test.want)
			}
		})
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
