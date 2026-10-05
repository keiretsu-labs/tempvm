package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Identity struct {
	LoginName string
	NodeName  string
	NodeTags  []string
	NodeIP    string
}

type IdentityResolver interface {
	Resolve(context.Context, string) (Identity, error)
}

type tailscaleIdentityResolver struct {
	client *http.Client
}

func newTailscaleIdentityResolver(socketPath string) IdentityResolver {
	if strings.TrimSpace(socketPath) == "" {
		return nil
	}
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 250 * time.Millisecond}).DialContext(ctx, "unix", socketPath)
		},
	}
	return &tailscaleIdentityResolver{client: &http.Client{
		Transport: transport,
		Timeout:   500 * time.Millisecond,
	}}
}

func (r *tailscaleIdentityResolver) Resolve(ctx context.Context, remoteAddr string) (Identity, error) {
	if strings.TrimSpace(remoteAddr) == "" {
		return Identity{}, fmt.Errorf("remote address is empty")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://tailscale.local/localapi/v0/whois?addr="+url.QueryEscape(remoteAddr), nil)
	if err != nil {
		return Identity{}, err
	}
	response, err := r.client.Do(request)
	if err != nil {
		return Identity{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("tailscale WhoIs returned HTTP %d", response.StatusCode)
	}

	var payload tailscaleWhoIsResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return Identity{}, fmt.Errorf("decode tailscale WhoIs response: %w", err)
	}
	return Identity{
		LoginName: payload.UserProfile.LoginName,
		NodeName:  payload.Node.Name,
		NodeTags:  append([]string(nil), payload.Node.Tags...),
		NodeIP:    firstNodeIP(payload.Node.Addresses),
	}, nil
}

type tailscaleWhoIsResponse struct {
	UserProfile struct {
		LoginName string `json:"LoginName"`
	} `json:"UserProfile"`
	Node struct {
		Name      string   `json:"Name"`
		Tags      []string `json:"Tags"`
		Addresses []string `json:"Addresses"`
	} `json:"Node"`
}

func firstNodeIP(addresses []string) string {
	for _, address := range addresses {
		if ip := net.ParseIP(strings.TrimSpace(address)); ip != nil {
			return ip.String()
		}
		if ip, _, err := net.ParseCIDR(strings.TrimSpace(address)); err == nil {
			return ip.String()
		}
	}
	return ""
}

func (i Identity) allowed(config Config) bool {
	if len(config.AllowLogins) == 0 && len(config.AllowTags) == 0 {
		return true
	}
	for _, allowed := range config.AllowLogins {
		if strings.EqualFold(strings.TrimSpace(allowed), strings.TrimSpace(i.LoginName)) && strings.TrimSpace(i.LoginName) != "" {
			return true
		}
	}
	for _, tag := range i.NodeTags {
		for _, allowed := range config.AllowTags {
			if strings.TrimSpace(tag) == strings.TrimSpace(allowed) && strings.TrimSpace(tag) != "" {
				return true
			}
		}
	}
	return false
}

func (i Identity) auditValues() (login, node, tags, ip string) {
	login = unknownValue(i.LoginName)
	node = unknownValue(i.NodeName)
	if len(i.NodeTags) == 0 {
		tags = "unknown"
	} else {
		tags = strings.Join(i.NodeTags, ",")
		if strings.TrimSpace(tags) == "" {
			tags = "unknown"
		}
	}
	ip = unknownValue(i.NodeIP)
	return
}

func unknownValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}
