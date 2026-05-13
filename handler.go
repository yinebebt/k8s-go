package main

import (
	"net/http"
	"strings"
	"time"
)

// statusRecorder wraps ResponseWriter to capture status code.
// Default 200 if handler never calls WriteHeader.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func live(w http.ResponseWriter, _ *http.Request) {
	// liveness: process alive. Only fail on unrecoverable state.
	_, err := w.Write([]byte("ok"))
	if err != nil {
		log.Error("error writing response", "error", err)
	}
}

func ready(w http.ResponseWriter, _ *http.Request) {
	// readiness: ready for traffic? false during startup + shutdown drain.
	if !isReady.Load() {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	_, err := w.Write([]byte("ok"))
	if err != nil {
		log.Error("error writing response", "error", err)
	}
}

func hello(w http.ResponseWriter, r *http.Request) {
	// Bearer auth using API_TOKEN from k8s Secret.
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if apiToken == "" || !strings.HasPrefix(auth, prefix) || auth[len(prefix):] != apiToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	_, err := w.Write([]byte("Hello, Welcome to Kubernetes world!"))
	if err != nil {
		log.Error("error writing response", "error", err)
	}
}

func withLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}
