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
