// Command kargo-loki-logs serves Kubernetes Job logs from Grafana Loki as
// plain text for Kargo's AnalysisRun log view.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/browntech/kargo-loki-logs/internal/config"
	"github.com/browntech/kargo-loki-logs/internal/loki"
	"github.com/browntech/kargo-loki-logs/internal/server"
)

func main() {
	if err := run(); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "kargo-loki-logs:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Parse(os.Args[1:], os.Getenv)
	if err != nil {
		return err
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	client := &loki.Client{
		BaseURL: cfg.LokiURL,
		Tenant:  cfg.Tenant,
		HTTP:    &http.Client{Timeout: cfg.LokiTimeout},
	}
	handler := server.New(server.Config{
		Source:     client,
		Labels:     server.Labels{Namespace: cfg.NamespaceLabel, Pod: cfg.PodLabel, Container: cfg.ContainerLabel},
		Since:      cfg.Since,
		Lookback:   cfg.Lookback,
		Limit:      cfg.Limit,
		WindowStep: cfg.WindowStep,
		Token:      cfg.Token,
		Logger:     logger,
	})

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      2*cfg.LokiTimeout + 10*time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.ListenAddr, "loki", cfg.LokiURL.Redacted(), "auth", cfg.Token != "")
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
