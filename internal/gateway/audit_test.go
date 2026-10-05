package gateway

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestSessionAuditJSONShape(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return attr
		},
	}))
	gateway := &Gateway{
		config: Config{Namespace: "tempvm"},
		logger: logger,
	}

	gateway.logSessionEvent(
		"session_stop",
		"tempvm-a1b2",
		Identity{
			LoginName: "person@example.com",
			NodeName:  "laptop.example.ts.net.",
			NodeTags:  []string{"tag:admin", "tag:k8s"},
			NodeIP:    "100.64.0.10",
		},
		"100.64.0.10",
		"linux",
		"tempvm-a1b2",
		"ttl",
		90*time.Second,
		nil,
	)

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode audit JSON %q: %v", output.String(), err)
	}
	wantStrings := map[string]string{
		"event":        "session_stop",
		"session":      "tempvm-a1b2",
		"login_name":   "person@example.com",
		"node_name":    "laptop.example.ts.net.",
		"node_tags":    "tag:admin,tag:k8s",
		"node_ip":      "100.64.0.10",
		"source_ip":    "100.64.0.10",
		"user":         "linux",
		"vm_namespace": "tempvm",
		"vm_name":      "tempvm-a1b2",
		"stop_reason":  "ttl",
	}
	for key, want := range wantStrings {
		if got := event[key]; got != want {
			t.Errorf("%s = %#v, want %q", key, got, want)
		}
	}
	if _, ok := event["ts"].(string); !ok {
		t.Errorf("ts = %#v, want an RFC3339 string", event["ts"])
	}
	if got := event["duration_seconds"]; got != float64(90) {
		t.Errorf("duration_seconds = %#v, want 90", got)
	}
	for _, forbidden := range []string{"private_key", "authorized_keys", "secret"} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatalf("audit output contains forbidden field %q: %s", forbidden, output.String())
		}
	}
}

func TestSessionAuditUsesUnknownIdentityFallback(t *testing.T) {
	var output bytes.Buffer
	gateway := &Gateway{
		config: Config{Namespace: "tempvm"},
		logger: slog.New(slog.NewJSONHandler(&output, nil)),
	}
	gateway.logSessionEvent("session_refused", "", Identity{}, "", "linux", "", "", 0, nil)

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"session", "login_name", "node_name", "node_tags", "node_ip", "source_ip", "vm_name"} {
		if got := event[key]; got != "unknown" {
			t.Errorf("%s = %#v, want unknown", key, got)
		}
	}
}
