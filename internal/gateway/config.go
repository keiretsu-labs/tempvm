package gateway

import (
	"errors"
	"strings"
	"time"
)

type Config struct {
	ListenAddress   string
	HealthAddress   string
	Namespace       string
	GuestImage      string
	RuntimeClass    string
	HostKeySecret   string
	BootTimeout     time.Duration
	MaxSessionTTL   time.Duration
	MaxSessions     int
	TailscaleSocket string
	AllowLogins     []string
	AllowTags       []string
}

func DefaultConfig() Config {
	return Config{
		ListenAddress: ":2222",
		HealthAddress: ":8080",
		Namespace:     "tempvm",
		RuntimeClass:  "kata-clh",
		HostKeySecret: "tempvm-host-key",
		BootTimeout:   5 * time.Minute,
		MaxSessionTTL: 4 * time.Hour,
		MaxSessions:   3,
	}
}

func (c Config) Validate() error {
	switch {
	case strings.TrimSpace(c.Namespace) == "":
		return errors.New("namespace must not be empty")
	case strings.TrimSpace(c.GuestImage) == "":
		return errors.New("guest image must not be empty")
	case strings.TrimSpace(c.RuntimeClass) == "":
		return errors.New("runtime class must not be empty")
	case strings.TrimSpace(c.HostKeySecret) == "":
		return errors.New("host key Secret must not be empty")
	case c.BootTimeout <= 0:
		return errors.New("boot timeout must be positive")
	case c.MaxSessionTTL < 0:
		return errors.New("max session TTL must not be negative")
	case c.MaxSessions < 0:
		return errors.New("max sessions must not be negative")
	case (len(c.AllowLogins) > 0 || len(c.AllowTags) > 0) && strings.TrimSpace(c.TailscaleSocket) == "":
		return errors.New("tailscale socket is required when an identity allow-list is configured")
	default:
		return nil
	}
}
