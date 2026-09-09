package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHello(t *testing.T) {
	const token = "test-token"
	oldToken := apiToken
	apiToken = token
	t.Cleanup(func() { apiToken = oldToken })

	tests := []struct {
		name          string
		authorization string
		wantStatus    int
	}{
		{
			name:          "valid bearer token",
			authorization: "Bearer " + token,
			wantStatus:    http.StatusOK,
		},
		{
			name:       "missing authorization",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:          "wrong token",
			authorization: "Bearer wrong-token",
			wantStatus:    http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/hello", nil)
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}
			rec := httptest.NewRecorder()

			hello(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestHealthHandlers(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		ready      bool
		wantStatus int
	}{
		{
			name:       "liveness is healthy",
			handler:    live,
			wantStatus: http.StatusOK,
		},
		{
			name:       "readiness is healthy",
			handler:    ready,
			ready:      true,
			wantStatus: http.StatusOK,
		},
		{
			name:       "readiness fails while draining",
			handler:    ready,
			wantStatus: http.StatusServiceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isReady.Store(tt.ready)
			t.Cleanup(func() { isReady.Store(false) })

			rec := httptest.NewRecorder()
			tt.handler(rec, httptest.NewRequest(http.MethodGet, "/", nil))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}
