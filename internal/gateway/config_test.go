package gateway

import (
	"testing"
	"time"
)

func TestConfigValidate(t *testing.T) {
	valid := Config{
		Namespace:     "tempvm",
		GuestImage:    "example.invalid/tempvm-linux@sha256:abc",
		RuntimeClass:  "kata-clh",
		HostKeySecret: "tempvm-host-key",
		BootTimeout:   time.Minute,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	tests := []struct {
		name string
		edit func(*Config)
	}{
		{"namespace", func(c *Config) { c.Namespace = "" }},
		{"image", func(c *Config) { c.GuestImage = "" }},
		{"runtime class", func(c *Config) { c.RuntimeClass = "" }},
		{"host key Secret", func(c *Config) { c.HostKeySecret = "" }},
		{"boot timeout", func(c *Config) { c.BootTimeout = 0 }},
		{"negative session TTL", func(c *Config) { c.MaxSessionTTL = -time.Second }},
		{"negative session cap", func(c *Config) { c.MaxSessions = -1 }},
		{"allow-list without LocalAPI", func(c *Config) { c.AllowLogins = []string{"user@example.com"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.edit(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

func TestDefaultConfigHardeningLimits(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxSessionTTL != 4*time.Hour {
		t.Fatalf("MaxSessionTTL = %s, want 4h", cfg.MaxSessionTTL)
	}
	if cfg.MaxSessions != 3 {
		t.Fatalf("MaxSessions = %d, want 3", cfg.MaxSessions)
	}
	if cfg.TailscaleSocket != "" {
		t.Fatalf("TailscaleSocket = %q, want disabled by default", cfg.TailscaleSocket)
	}
}

func TestConfigAllowsIdentityAllowListWithLocalAPI(t *testing.T) {
	cfg := DefaultConfig()
	cfg.GuestImage = "example.invalid/tempvm-linux@sha256:abc"
	cfg.TailscaleSocket = "/var/run/tailscale/tailscaled.sock"
	cfg.AllowTags = []string{"tag:admin"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid identity config rejected: %v", err)
	}
}
