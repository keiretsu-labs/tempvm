package gateway

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestIdentityAllowList(t *testing.T) {
	identity := Identity{
		LoginName: "Person@Example.com",
		NodeTags:  []string{"tag:developer", "tag:k8s"},
	}
	tests := []struct {
		name   string
		config Config
		want   bool
	}{
		{name: "no allow-list", config: Config{}, want: true},
		{name: "login", config: Config{AllowLogins: []string{"person@example.com"}}, want: true},
		{name: "tag", config: Config{AllowTags: []string{"tag:k8s"}}, want: true},
		{name: "not allowed", config: Config{AllowLogins: []string{"other@example.com"}}, want: false},
		{name: "unknown identity", config: Config{AllowTags: []string{"tag:k8s"}}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := identity
			if test.name == "unknown identity" {
				candidate = Identity{}
			}
			if got := candidate.allowed(test.config); got != test.want {
				t.Fatalf("allowed() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestFirstNodeIP(t *testing.T) {
	if got := firstNodeIP([]string{"not-an-ip", "100.64.0.10/32", "fd7a:115c:a1e0::1"}); got != "100.64.0.10" {
		t.Fatalf("firstNodeIP() = %q, want 100.64.0.10", got)
	}
}

func TestTailscaleWhoIsResolver(t *testing.T) {
	resolver := &tailscaleIdentityResolver{client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.URL.Path; got != "/localapi/v0/whois" {
			t.Fatalf("WhoIs path = %q", got)
		}
		if got := request.URL.Query().Get("addr"); got != "100.64.0.10:2222" {
			t.Fatalf("WhoIs addr = %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
				"UserProfile":{"LoginName":"person@example.com"},
				"Node":{"Name":"laptop.example.ts.net.","Tags":["tag:developer"],"Addresses":["100.64.0.10/32"]}
			}`)),
			Header: make(http.Header),
		}, nil
	})}}

	identity, err := resolver.Resolve(context.Background(), "100.64.0.10:2222")
	if err != nil {
		t.Fatal(err)
	}
	if identity.LoginName != "person@example.com" || identity.NodeName != "laptop.example.ts.net." || identity.NodeIP != "100.64.0.10" {
		t.Fatalf("identity = %#v", identity)
	}
	if len(identity.NodeTags) != 1 || identity.NodeTags[0] != "tag:developer" {
		t.Fatalf("node tags = %#v", identity.NodeTags)
	}
}

func TestTailscaleResolverDisabledWithoutSocket(t *testing.T) {
	if resolver := newTailscaleIdentityResolver(""); resolver != nil {
		t.Fatalf("resolver = %#v, want nil", resolver)
	}
}
