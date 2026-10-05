package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/keiretsu-labs/tempvm/internal/gateway"
)

type staticStatusProvider struct {
	status gateway.Status
}

func (p staticStatusProvider) Status() gateway.Status {
	return p.status
}

func TestLandingPage(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()

	healthHandler(staticStatusProvider{status: gateway.Status{
		ActiveSessions: 2,
		MaxSessions:    3,
		MaxSessionTTL:  4 * time.Hour,
	}}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q", contentType)
	}
	body := response.Body.String()
	if !strings.Contains(body, "ssh linux@tempvm") {
		t.Fatal("landing page does not contain the simple SSH command")
	}
	if !strings.Contains(body, "2 / 3") || !strings.Contains(body, "4h0m0s") {
		t.Fatalf("landing page does not contain session status: %q", body)
	}
	if strings.Contains(body, "StrictHostKeyChecking") || strings.Contains(body, "UserKnownHostsFile") {
		t.Fatal("landing page contains host-check bypass flags")
	}
}

func TestHealthRoutes(t *testing.T) {
	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			response := httptest.NewRecorder()

			healthHandler(staticStatusProvider{status: gateway.Status{
				ActiveSessions: 2,
				MaxSessions:    3,
				MaxSessionTTL:  4 * time.Hour,
			}}).ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("%s returned status=%d body=%q", path, response.Code, response.Body.String())
			}
			var body struct {
				Status         string `json:"status"`
				ActiveSessions int    `json:"active_sessions"`
				MaxSessions    int    `json:"max_sessions"`
				MaxSessionTTL  string `json:"max_session_ttl"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode %s response: %v", path, err)
			}
			if body.Status != "ok" || body.ActiveSessions != 2 || body.MaxSessions != 3 || body.MaxSessionTTL != "4h0m0s" {
				t.Fatalf("%s returned %#v", path, body)
			}
		})
	}
}

func TestLandingPageRejectsUnknownPaths(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/missing", nil)
	response := httptest.NewRecorder()

	healthHandler().ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}
