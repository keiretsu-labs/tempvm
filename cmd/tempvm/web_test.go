package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLandingPage(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()

	healthHandler().ServeHTTP(response, request)

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
	if strings.Contains(body, "StrictHostKeyChecking") || strings.Contains(body, "UserKnownHostsFile") {
		t.Fatal("landing page contains host-check bypass flags")
	}
}

func TestHealthRoutes(t *testing.T) {
	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			response := httptest.NewRecorder()

			healthHandler().ServeHTTP(response, request)

			if response.Code != http.StatusOK || response.Body.String() != "ok\n" {
				t.Fatalf("%s returned status=%d body=%q", path, response.Code, response.Body.String())
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
