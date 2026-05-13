package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	version string
	log     *slog.Logger
	// loaded from API_TOKEN env (k8s Secret); empty means auth disabled.
	apiToken string
	// ready flips to true once server accepts traffic, false on shutdown
	isReady atomic.Bool
)

func main() {
	logLevel := slog.LevelInfo
	if s := os.Getenv("LOG_LEVEL"); s != "" {
		if err := logLevel.UnmarshalText([]byte(s)); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "invalid LOG_LEVEL %q, defaulting to INFO: %v\n", s, err)
			logLevel = slog.LevelInfo
		}
	}

	log = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		AddSource: true,
		Level:     logLevel,
	}))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	apiToken = os.Getenv("API_TOKEN")
	if apiToken == "" {
		log.Warn("API_TOKEN not set - /hello will reject all requests")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/hello", hello)
	mux.HandleFunc("/livez", live)
	mux.HandleFunc("/readyz", ready)

	server := &http.Server{
		Addr:    ":" + port,
		Handler: withLog(mux),
	}

	// channel to listen for OS signals
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Info("k8s-go is running", "version", version, "port", port)
		isReady.Store(true)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-stop
	log.Info("shutting down server...")

	// flip readiness off so k8s removes pod from Service endpoints before we stop accepting connections.
	// Sleep > readiness probe period so in-flight traffic stops arriving.
	isReady.Store(false)
	time.Sleep(2 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Error("server forced to shutdown", "error", err)
	}

	log.Info("server exited gracefully")
}
