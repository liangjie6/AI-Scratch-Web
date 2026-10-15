// Command server is the AI Scratch Web Go backend entrypoint. It replaces the
// uvicorn app.main:app process from the Python reference implementation.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ai-scratch-web/backend/internal/config"
	"github.com/ai-scratch-web/backend/internal/httpapi"
)

func main() {
	defaultHost := envOr("AI_SCRATCH_HOST", "127.0.0.1")
	defaultPort := envOr("AI_SCRATCH_PORT", "8000")
	host := flag.String("host", defaultHost, "interface to bind")
	port := flag.String("port", defaultPort, "TCP port to bind")
	logLevel := flag.String("log-level", envOr("AI_SCRATCH_LOG_LEVEL", "INFO"), "application log level")
	flag.Parse()

	logger := newLogger(*logLevel)
	slog.SetDefault(logger)

	settings, err := config.Load()
	if err != nil {
		logger.Error("configuration_invalid", "error", err.Error())
		os.Exit(1)
	}

	server := httpapi.New(settings, logger)
	if err := server.Startup(); err != nil {
		logger.Error("startup_failed", "error", err.Error())
		os.Exit(1)
	}
	httpServer := &http.Server{
		Addr:              net.JoinHostPort(*host, *port),
		Handler:           server.Handler(),
		ReadHeaderTimeout: 30 * time.Second,
	}

	go func() {
		logger.Info("server_started", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server_failed", "error", err.Error())
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("server_shutdown_failed", "error", err.Error())
	}
	server.Shutdown(ctx)
	logger.Info("server_stopped")
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func newLogger(level string) *slog.Logger {
	var parsed slog.Level
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG":
		parsed = slog.LevelDebug
	case "WARN", "WARNING":
		parsed = slog.LevelWarn
	case "ERROR":
		parsed = slog.LevelError
	default:
		parsed = slog.LevelInfo
	}
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: parsed})
	return slog.New(handler)
}

var _ = strconv.Itoa
